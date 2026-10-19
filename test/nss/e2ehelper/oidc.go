package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// idpKeyID is the kid on the JWKS key and on every token the provider
// signs.
const idpKeyID = "nss-e2e"

// identityProvider is a minimal OIDC provider, the same shape as the e2e
// suite's (test/e2e/harness/idp.go) without its testing.T: discovery, an
// authorization endpoint whose form signs in whatever username is posted,
// the code exchange and a JWKS. go-oidc in ssoosshd verifies the tokens it
// signs, so a login through it takes the server's real path. Like a real
// provider it knows its one client's registered redirect URI and sends a
// browser nowhere else.
type identityProvider struct {
	base string
	key  *rsa.PrivateKey
	// redirectURI is the registered redirect URI, exactly as the client
	// must send it; redirect is it parsed.
	redirectURI string
	redirect    *url.URL

	mu    sync.Mutex
	codes map[string]pendingAuth
}

// pendingAuth is what one authorization code stands for, consumed once by
// the token endpoint.
type pendingAuth struct {
	nonce    string
	username string
}

// newIdentityProvider makes a provider that names itself base, with a
// fresh signing key, for a client whose registered redirect URI is
// redirectURI, an absolute URL.
func newIdentityProvider(base, redirectURI string) (*identityProvider, error) {
	redirect, err := url.Parse(redirectURI)
	if err != nil || !redirect.IsAbs() || redirect.Host == "" {
		return nil, fmt.Errorf("redirect URI %q is not an absolute URL", redirectURI)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return nil, fmt.Errorf("generate the signing key: %w", err)
	}
	return &identityProvider{base: base, key: key, redirectURI: redirectURI, redirect: redirect, codes: map[string]pendingAuth{}}, nil
}

// handler routes the provider's four endpoints.
func (p *identityProvider) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /jwks", p.jwks)
	return mux
}

// discovery serves the fields go-oidc reads.
func (p *identityProvider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                p.base,
		"authorization_endpoint":                p.base + "/authorize",
		"token_endpoint":                        p.base + "/token",
		"jwks_uri":                              p.base + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

// authorize renders a one-field form on GET and, on POST, mints a code for
// the posted username and redirects back to the client. A redirect_uri
// that is not exactly the registered one is refused with 400 on either,
// before anything is minted, as a real provider refuses it.
func (p *identityProvider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("redirect_uri") != p.redirectURI {
		http.Error(w, "redirect_uri is not the registered one", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html><form method="POST"><input name="username"><button>Sign in</button></form>`)
		return
	}
	if err := r.ParseForm(); err != nil || r.PostFormValue("username") == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}
	code, err := randomHex(16)
	if err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		http.Error(w, "generate a code", http.StatusInternalServerError)
		return
	}
	p.mu.Lock()
	p.codes[code] = pendingAuth{nonce: q.Get("nonce"), username: r.PostFormValue("username")}
	p.mu.Unlock()
	// The registered URI, not the request's copy of it: equal by the
	// check above, and nothing the request sent decides where it goes.
	dest := *p.redirect
	values := dest.Query()
	values.Set("code", code)
	values.Set("state", q.Get("state"))
	dest.RawQuery = values.Encode()
	http.Redirect(w, r, dest.String(), http.StatusFound)
}

// token exchanges a code for a signed ID token naming its user.
func (p *identityProvider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	code := r.PostFormValue("code")
	p.mu.Lock()
	pending, ok := p.codes[code]
	delete(p.codes, code)
	p.mu.Unlock()
	if !ok {
		http.Error(w, "unknown code", http.StatusBadRequest)
		return
	}
	clientID, _, ok := r.BasicAuth()
	if !ok {
		clientID = r.PostFormValue("client_id")
	}
	idToken, err := p.sign(clientID, pending, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"access_token": "nss-e2e-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	})
}

// sign makes the ID token for pending, issued to clientID at now.
func (p *identityProvider) sign(clientID string, pending pendingAuth, now time.Time) (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", idpKeyID))
	if err != nil {
		// not covered: an RSA key always makes an RS256 signer.
		return "", fmt.Errorf("build the signer: %w", err)
	}
	claims := jwt.Claims{
		Issuer:   p.base,
		Subject:  pending.username,
		Audience: jwt.Audience{clientID},
		Expiry:   jwt.NewNumericDate(now.Add(time.Hour)),
		IssuedAt: jwt.NewNumericDate(now),
	}
	private := map[string]any{"preferred_username": pending.username, "email": pending.username + "@example.test"}
	if pending.nonce != "" {
		private["nonce"] = pending.nonce
	}
	return jwt.Signed(signer).Claims(claims).Claims(private).Serialize()
}

// jwks serves the public half of the signing key.
func (p *identityProvider) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: &p.key.PublicKey, KeyID: idpKeyID, Algorithm: "RS256", Use: "sig",
	}}})
}

// writeJSON writes body as a JSON response.
func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		fmt.Fprintln(os.Stderr, "e2ehelper: write a response:", err)
	}
}

// randomHex returns n random bytes in hex.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// runOIDC serves the provider on -listen until ctx ends.
func runOIDC(ctx context.Context, args []string, _ io.Writer) error {
	fs := flag.NewFlagSet("oidc", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:9000", "loopback address to serve on")
	redirectURI := fs.String("redirect-uri", "https://server:8443/auth/callback", "the client's registered redirect URI")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := newIdentityProvider("http://"+*listen, *redirectURI)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *listen, err)
	}
	srv := &http.Server{Handler: p.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// runLogin signs -user in to the ssoosshd at -server through the provider
// and prints the username /api/users/me then reports. The server's
// certificate is trusted from -ca.
func runLogin(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", "https://server:8443", "ssoosshd's public URL")
	caFile := fs.String("ca", "tls.crt", "the server certificate to trust")
	user := fs.String("user", "", "the username to sign in as")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := loginClient(*caFile)
	if err != nil {
		return err
	}
	username, err := login(ctx, client, *server, *user)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, username)
	return err
}

// loginClient is an HTTP client with a cookie jar that trusts only the
// certificate in caFile.
func loginClient(caFile string) (*http.Client, error) {
	pemBytes, err := os.ReadFile(filepath.Clean(caFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", caFile, err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("%s holds no certificate", caFile)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		// not covered: cookiejar.New never fails without options.
		return nil, fmt.Errorf("make a cookie jar: %w", err)
	}
	return &http.Client{
		Jar:       jar,
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
	}, nil
}

// login walks the authorization code flow: /auth/login redirects to the
// provider's form, the form post redirects back to the server's callback,
// and the callback lands on /api/users/me, whose username it returns.
func login(ctx context.Context, client *http.Client, server, user string) (string, error) {
	if user == "" {
		return "", errors.New("-user is required")
	}
	start := server + "/auth/login?return_to=" + url.QueryEscape("/api/users/me")
	resp, err := get(ctx, client, start)
	if err != nil {
		return "", fmt.Errorf("start the login: %w", err)
	}
	authorize := resp.Request.URL.String()
	if err := resp.Body.Close(); err != nil {
		return "", fmt.Errorf("start the login: %w", err)
	}

	form := strings.NewReader(url.Values{"username": {user}}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authorize, form)
	if err != nil {
		return "", fmt.Errorf("build the form post: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(req)
	if err != nil {
		return "", fmt.Errorf("submit the provider's form: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048)) //nolint:errcheck // best-effort detail for the error.
		return "", fmt.Errorf("login ended at %s with %d: %s", resp.Request.URL, resp.StatusCode, body)
	}
	var me struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return "", fmt.Errorf("decode /api/users/me: %w", err)
	}
	return me.Data.Username, nil
}

// get is a GET with ctx.
func get(ctx context.Context, client *http.Client, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}
