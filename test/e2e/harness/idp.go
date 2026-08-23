//go:build e2e || resilience || load

// Package harness provides the process apparatus for the end-to-end suite:
// a real OIDC identity provider, a real ssoosshd instance, a real ssoossh
// client, a private ssh-agent, and (tier 3) a real sshd. See
// docs/dev/e2e-testing-plan.md for the design this implements.
package harness

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// idpKeyID is the "kid" advertised in the JWKS and stamped on every signed
// ID token, so the verifier's key lookup has something to match on.
const idpKeyID = "e2e-test-key"

// pendingAuth is what an authorization code resolves to: the claims the
// "user" submitted on the IdP's login form, kept server-side (never in the
// code itself) and consumed exactly once by the token endpoint.
type pendingAuth struct {
	redirectURI string
	nonce       string
	claims      map[string]any
}

// IdentityProvider is a minimal, real OIDC provider: discovery, an
// authorization endpoint with a genuine HTML form, code exchange, and a
// JWKS, signing RS256 ID tokens with a key generated per run.
//
// Deliberately real rather than a stub inside ssoosshd: go-oidc performs
// discovery, validates the token against the JWKS, and checks the nonce, so
// this exercises the server's actual authentication path, the same one a
// real provider like pocket-id would face.
type IdentityProvider struct {
	srv *httptest.Server
	key *rsa.PrivateKey
	kid string

	mu    sync.Mutex
	codes map[string]pendingAuth
}

// NewIdentityProvider starts the IdP on an httptest.Server (which solves
// port allocation for free) and registers its teardown on t.Cleanup.
func NewIdentityProvider(t *testing.T) *IdentityProvider {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("harness: failed to generate IdP signing key: %v", err)
	}

	idp := &IdentityProvider{
		key:   key,
		kid:   idpKeyID,
		codes: make(map[string]pendingAuth),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", idp.handleDiscovery)
	mux.HandleFunc("/authorize", idp.handleAuthorize)
	mux.HandleFunc("/token", idp.handleToken)
	mux.HandleFunc("/jwks", idp.handleJWKS)

	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)

	return idp
}

// URL returns the IdP's base URL — the provider_url ssoosshd is configured
// with, and the issuer it signs tokens as.
func (idp *IdentityProvider) URL() string { return idp.srv.URL }

// handleDiscovery serves /.well-known/openid-configuration. Only the fields
// go-oidc's NewProvider actually reads are populated.
func (idp *IdentityProvider) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	doc := map[string]any{
		"issuer":                                idp.URL(),
		"authorization_endpoint":                idp.URL() + "/authorize",
		"token_endpoint":                        idp.URL() + "/token",
		"jwks_uri":                              idp.URL() + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc) //nolint:errcheck // best-effort response write, test harness
}

// handleAuthorize serves both halves of the authorization endpoint on one
// path, branching on method: GET renders a real HTML form (so tier 2's
// browser driver has something genuine to interact with — this is not a
// bypass, go-oidc's actual verification path runs regardless of how the
// form gets submitted); POST processes it, minting a one-time code bound to
// whatever identity the form claimed.
//
// The OAuth parameters (client_id, redirect_uri, state, nonce, scope) live
// in the query string throughout — the form's action is empty, so a normal
// browser or a plain http.Client posting to the same URL carries them
// forward automatically.
func (idp *IdentityProvider) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	nonce := q.Get("nonce")

	if r.Method == http.MethodGet {
		idp.renderLoginForm(w)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	username := r.PostFormValue("username")
	if username == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}

	claims := map[string]any{
		"sub":                username,
		"preferred_username": username,
	}
	if email := r.PostFormValue("email"); email != "" {
		claims["email"] = email
	}
	if groups := r.PostForm["groups"]; len(groups) > 0 {
		anyGroups := make([]any, len(groups))
		for i, g := range groups {
			anyGroups[i] = g
		}
		claims["groups"] = anyGroups
	}

	// extra_claims is a JSON object merged into the ID token verbatim, so a
	// test can stamp arbitrary additional claims (e.g. the ones
	// authentication.fields.extra maps into key ID templates) without the
	// form growing a field per claim shape.
	if extraJSON := r.PostFormValue("extra_claims"); extraJSON != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(extraJSON), &extra); err != nil {
			http.Error(w, "extra_claims is not a JSON object", http.StatusBadRequest)
			return
		}
		for k, v := range extra {
			claims[k] = v
		}
	}

	code, err := randomHex(16)
	if err != nil {
		http.Error(w, "failed to generate authorization code", http.StatusInternalServerError)
		return
	}

	idp.mu.Lock()
	idp.codes[code] = pendingAuth{redirectURI: redirectURI, nonce: nonce, claims: claims}
	idp.mu.Unlock()

	dest := fmt.Sprintf("%s?code=%s&state=%s", redirectURI, code, state)
	http.Redirect(w, r, dest, http.StatusFound) //nolint:gosec // this *is* the OAuth redirect_uri flow being simulated; this IdP is process-local test fixture, never network-exposed
}

// renderLoginForm writes a minimal real HTML form: a username field, a
// repeatable groups field, and a submit button. data-testid attributes let
// the browser tier select on something stable rather than prose.
func (idp *IdentityProvider) renderLoginForm(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<body>
  <form method="POST" data-testid="idp-login-form">
    <input type="text" name="username" data-testid="idp-username" />
    <input type="text" name="groups" data-testid="idp-groups" />
    <button type="submit" data-testid="idp-submit">Sign in</button>
  </form>
</body>
</html>`)
}

// handleToken serves POST /token: the standard authorization_code exchange.
// Client credentials are accepted either as HTTP Basic auth or as POST body
// fields, since golang.org/x/oauth2's AuthStyleAutoDetect may pick either
// and this only needs to accept what it actually sends, not police it —
// authenticating the client is not what this harness is testing.
func (idp *IdentityProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	code := r.PostFormValue("code")
	idp.mu.Lock()
	pending, ok := idp.codes[code]
	if ok {
		delete(idp.codes, code)
	}
	idp.mu.Unlock()
	if !ok {
		http.Error(w, "unknown or already-used authorization code", http.StatusBadRequest)
		return
	}

	clientID, _, ok := r.BasicAuth()
	if !ok {
		clientID = r.PostFormValue("client_id")
	}

	now := time.Now()
	claims := jwt.Claims{
		Issuer:   idp.URL(),
		Subject:  fmt.Sprint(pending.claims["sub"]),
		Audience: jwt.Audience{clientID},
		Expiry:   jwt.NewNumericDate(now.Add(time.Hour)),
		IssuedAt: jwt.NewNumericDate(now),
	}
	privateClaims := map[string]any{}
	for k, v := range pending.claims {
		if k == "sub" {
			continue
		}
		privateClaims[k] = v
	}
	if pending.nonce != "" {
		privateClaims["nonce"] = pending.nonce
	}

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: idp.key}, (&jose.SignerOptions{}).
		WithType("JWT").
		WithHeader("kid", idp.kid))
	if err != nil {
		http.Error(w, "failed to build signer", http.StatusInternalServerError)
		return
	}

	idToken, err := jwt.Signed(signer).Claims(claims).Claims(privateClaims).Serialize()
	if err != nil {
		http.Error(w, "failed to sign id_token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // best-effort response write, test harness
		"access_token": "e2e-test-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	})
}

// handleJWKS serves the public half of the signing key, so go-oidc's
// verifier can validate the ID tokens minted by handleToken.
func (idp *IdentityProvider) handleJWKS(w http.ResponseWriter, r *http.Request) {
	set := jose.JSONWebKeySet{
		Keys: []jose.JSONWebKey{
			{
				Key:       &idp.key.PublicKey,
				KeyID:     idp.kid,
				Algorithm: "RS256",
				Use:       "sig",
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(set) //nolint:errcheck // best-effort response write, test harness
}

// randomHex returns a random hex string n bytes long before encoding.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
