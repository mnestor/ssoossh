package service

// Test methodology: a real OIDC provider, not a fake — an httptest.Server
// serving genuine discovery/JWKS/token documents and a genuinely
// RS256-signed ID token (via go-jose, already a transitive dependency of
// go-oidc). RS256 specifically: oidc.IDTokenVerifier defaults to accepting
// only RS256 when SupportedSigningAlgs isn't set, which HandleCallback's
// verifier construction leaves at that default. NewAuthService does real
// provider discovery against it; AuthorizationURL and HandleCallback run
// against the real oauth2.Config and oidc.IDTokenVerifier that discovery
// produces. This is what proves the wiring in NewAuthService actually
// works, not just that HandleCallback's claim-mapping logic is correct in
// isolation.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	josejwt "github.com/go-jose/go-jose/v4"

	"golang.org/x/oauth2"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// fakeOIDCProvider is a real OIDC discovery/JWKS/token server backed by a
// throwaway RSA key, for testing NewAuthService, AuthorizationURL, and
// HandleCallback against genuine provider wiring.
type fakeOIDCProvider struct {
	srv    *httptest.Server
	priv   *rsa.PrivateKey
	nextID string // the next ID token to hand back from /token, set per test

	tokenExchangeFails bool // when true, /token responds with an error status
	omitIDToken        bool // when true, /token's response has no id_token field at all

	// tokenForm is the form the last /token request carried, so a test can
	// assert on what the exchange actually sent — code_verifier in
	// particular, which is the only externally visible difference PKCE
	// makes to the exchange.
	tokenForm url.Values
}

func newFakeOIDCProvider(t *testing.T) *fakeOIDCProvider {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate provider signing key: %v", err)
	}

	p := &fakeOIDCProvider{priv: priv}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 p.srv.URL,
			"authorization_endpoint": p.srv.URL + "/authorize",
			"token_endpoint":         p.srv.URL + "/token",
			"jwks_uri":               p.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		jwk := josejwt.JSONWebKey{Key: &priv.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(josejwt.JSONWebKeySet{Keys: []josejwt.JSONWebKey{jwk}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.tokenForm = r.Form
		if p.tokenExchangeFails {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
			return
		}
		body := map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		if !p.omitIDToken {
			body["id_token"] = p.nextID
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})

	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// signIDToken builds and signs an ID token with the given claims merged
// over the required standard ones (iss, aud, exp, iat), returning its
// compact serialization.
func (p *fakeOIDCProvider) signIDToken(t *testing.T, subject, audience, nonce string, extra map[string]any) string {
	t.Helper()

	now := time.Now()
	claims := map[string]any{
		"iss":   p.srv.URL,
		"sub":   subject,
		"aud":   audience,
		"exp":   now.Add(time.Hour).Unix(),
		"iat":   now.Unix(),
		"nonce": nonce,
	}
	for k, v := range extra {
		claims[k] = v
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("failed to marshal ID token claims: %v", err)
	}

	signer, err := josejwt.NewSigner(
		josejwt.SigningKey{Algorithm: josejwt.RS256, Key: p.priv},
		(&josejwt.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatalf("failed to build signer: %v", err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("failed to sign ID token: %v", err)
	}
	serialized, err := jws.CompactSerialize()
	if err != nil {
		t.Fatalf("failed to serialize ID token: %v", err)
	}
	return serialized
}

// newTestAuthConfig returns a config.Config pointed at provider, with the
// minimum fields NewAuthService requires.
func newTestAuthConfig(provider *fakeOIDCProvider, clientID string) *config.Config {
	c := &config.Config{}
	c.AuthConfig.ProviderURL = provider.srv.URL
	c.AuthConfig.ClientID = clientID
	c.AuthConfig.Fields.Subject = "sub"
	c.AuthConfig.Fields.Username = "preferred_username"
	c.AuthConfig.Fields.Name = "name"
	c.AuthConfig.Fields.Email = "email"
	c.AuthConfig.Fields.Groups = "groups"
	c.HTTP.PublicURL = "https://ssh.example.com"
	return c
}

func TestNewAuthService_ShouldDiscoverTheProviderAndConstruct(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	db := newTestUserDB(t)

	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), db, provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}
	if svc == nil {
		t.Fatal("NewAuthService() returned a nil service with a nil error")
	}
}

func TestNewAuthService_ShouldRejectMissingRequiredConfig(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	db := newTestUserDB(t)

	tests := []struct {
		name    string
		mutate  func(c *config.Config)
		wantErr string
	}{
		{name: "should require provider_url", mutate: func(c *config.Config) { c.AuthConfig.ProviderURL = "" }, wantErr: "provider_url"},
		{name: "should require client_id", mutate: func(c *config.Config) { c.AuthConfig.ClientID = "" }, wantErr: "client_id"},
		{name: "should require fields.username", mutate: func(c *config.Config) { c.AuthConfig.Fields.Username = "" }, wantErr: "fields.username"},
		{name: "should require fields.subject", mutate: func(c *config.Config) { c.AuthConfig.Fields.Subject = "" }, wantErr: "fields.subject"},
		{name: "should require http.public_url", mutate: func(c *config.Config) { c.HTTP.PublicURL = "" }, wantErr: "public_url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newTestAuthConfig(provider, "client-1")
			tt.mutate(c)

			_, err := NewAuthService(context.Background(), c, db, provider.srv.Client())
			if err == nil {
				t.Fatalf("NewAuthService() error = nil, want an error mentioning %q", tt.wantErr)
			}
		})
	}
}

// TestNewAuthService_ShouldTrimAWellKnownSuffixFromTheProviderURL covers
// the convenience of pasting the full discovery document URL (as copied
// straight from a provider's docs) into provider_url — NewAuthService trims
// the well-known suffix before discovery rather than requiring the bare
// issuer URL.
func TestNewAuthService_ShouldTrimAWellKnownSuffixFromTheProviderURL(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	c := newTestAuthConfig(provider, "client-1")
	c.AuthConfig.ProviderURL = provider.srv.URL + "/.well-known/openid-configuration"

	if _, err := NewAuthService(context.Background(), c, newTestUserDB(t), provider.srv.Client()); err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}
}

func TestNewAuthService_ShouldRejectAnUnreachableProvider(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.AuthConfig.ProviderURL = "http://127.0.0.1:1"
	c.AuthConfig.ClientID = "client-1"
	c.AuthConfig.Fields.Username = "preferred_username"
	c.HTTP.PublicURL = "https://ssh.example.com"

	if _, err := NewAuthService(context.Background(), c, newTestUserDB(t), nil); err == nil {
		t.Error("NewAuthService() error = nil, want an error for an unreachable provider")
	}
}

func TestAuthorizationURL_ShouldEmbedStateAndAFreshNonce(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	authURL, nonce1, pkceVerifier1, err := svc.AuthorizationURL(context.Background(), "state-1")
	if err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}
	if nonce1 == "" {
		t.Fatal("AuthorizationURL() returned an empty nonce")
	}
	if pkceVerifier1 == "" {
		t.Fatal("AuthorizationURL() returned an empty PKCE verifier")
	}
	if !strings.Contains(authURL, "state=state-1") {
		t.Errorf("authURL = %q, want it to contain state=state-1", authURL)
	}
	if !strings.Contains(authURL, "nonce="+nonce1) {
		t.Errorf("authURL = %q, want it to contain the generated nonce", authURL)
	}

	_, nonce2, pkceVerifier2, err := svc.AuthorizationURL(context.Background(), "state-2")
	if err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}
	if nonce1 == nonce2 {
		t.Error("expected two calls to AuthorizationURL to generate distinct nonces")
	}
	if pkceVerifier1 == pkceVerifier2 {
		t.Error("expected two calls to AuthorizationURL to generate distinct PKCE verifiers")
	}
}
func TestHandleCallback_ShouldExchangeAndUpsertTheUser(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	db := newTestUserDB(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), db, provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	provider.nextID = provider.signIDToken(t, "sub-alice", "client-1", "nonce-1", map[string]any{
		"preferred_username": "alice",
		"email":              "alice@example.com",
		"groups":             []string{"admins", "devs"},
	})

	identity, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier())
	if err != nil {
		t.Fatalf("HandleCallback() error = %v", err)
	}
	if identity.Subject != "sub-alice" {
		t.Errorf("got Subject %q, want %q", identity.Subject, "sub-alice")
	}
	if identity.Username != "alice" {
		t.Errorf("got Username %q, want %q", identity.Username, "alice")
	}
	if identity.Email != "alice@example.com" {
		t.Errorf("got Email %q, want %q", identity.Email, "alice@example.com")
	}
	if len(identity.Groups) != 2 {
		t.Errorf("got Groups %v, want 2 entries", identity.Groups)
	}

	var count int64
	if err := db.Model(&model.User{}).Where("subject = ?", "sub-alice").Count(&count).Error; err != nil {
		t.Fatalf("failed to count users: %v", err)
	}
	if count != 1 {
		t.Errorf("expected the user to be upserted, found %d rows", count)
	}
}

// TestHandleCallback_ShouldExtractAndPersistConfiguredExtraFields covers
// the extra-fields pipeline end to end: configured mapping -> ID token
// claims -> Identity.Extra -> JSON on the users row (extra_fields).
func TestHandleCallback_ShouldExtractAndPersistConfiguredExtraFields(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	db := newTestUserDB(t)
	c := newTestAuthConfig(provider, "client-1")
	c.AuthConfig.Fields.Extra = map[string]string{
		"dept":     "department",
		"accounts": "altAccounts",
		"absent":   "notInToken",
	}
	svc, err := NewAuthService(context.Background(), c, db, provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	provider.nextID = provider.signIDToken(t, "sub-alice", "client-1", "nonce-1", map[string]any{
		"preferred_username": "alice",
		"department":         "eng",
		"altAccounts":        []string{"a-alice", "b-alice"},
	})

	identity, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier())
	if err != nil {
		t.Fatalf("HandleCallback() error = %v", err)
	}

	if got := identity.Extra["dept"].String(); got != "eng" {
		t.Errorf("Extra[dept] = %q, want %q", got, "eng")
	}
	if got := identity.Extra["accounts"].String(); got != "a-alice,b-alice" {
		t.Errorf("Extra[accounts] = %q, want %q", got, "a-alice,b-alice")
	}
	if got := identity.Extra["absent"].String(); got != "MISSING" {
		t.Errorf("Extra[absent] = %q, want %q", got, "MISSING")
	}

	var row model.User
	if err := db.First(&row, "subject = ?", "sub-alice").Error; err != nil {
		t.Fatalf("failed to load upserted user: %v", err)
	}
	var stored map[string]extraValue
	if err := json.Unmarshal([]byte(row.ExtraFields), &stored); err != nil {
		t.Fatalf("failed to decode persisted extra_fields %q: %v", row.ExtraFields, err)
	}
	if got := stored["dept"].String(); got != "eng" {
		t.Errorf("persisted dept = %q, want %q", got, "eng")
	}
	if got := stored["accounts"].Join(";"); got != "a-alice;b-alice" {
		t.Errorf("persisted accounts joined = %q, want %q", got, "a-alice;b-alice")
	}
}

func TestHandleCallback_ShouldSurfaceATokenExchangeFailure(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	provider.tokenExchangeFails = true
	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	if _, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier()); err == nil {
		t.Error("HandleCallback() error = nil, want error when the token exchange fails")
	}
}

func TestHandleCallback_ShouldRejectATokenResponseMissingIDToken(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	provider.omitIDToken = true
	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	if _, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier()); err == nil {
		t.Error("HandleCallback() error = nil, want error when the token response has no id_token")
	}
}

// TestHandleCallback_ShouldRejectAnUnverifiableIDToken covers the
// verifier.Verify error branch: an ID token signed by a key the provider's
// own JWKS doesn't contain (a different, throwaway signer here) must be
// rejected rather than trusted.
func TestHandleCallback_ShouldRejectAnUnverifiableIDToken(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	untrustedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate an untrusted signing key: %v", err)
	}
	rogue := &fakeOIDCProvider{srv: provider.srv, priv: untrustedKey}
	provider.nextID = rogue.signIDToken(t, "sub-alice", "client-1", "nonce-1", map[string]any{"preferred_username": "alice"})

	if _, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier()); err == nil {
		t.Error("HandleCallback() error = nil, want error for an ID token signed by an untrusted key")
	}
}

// TestHandleCallback_ShouldSurfaceAnUpsertFailure covers HandleCallback's
// own passthrough of upsertUser's error, exercised by closing the
// underlying DB connection after construction so the later Create call
// fails with a real error.
func TestHandleCallback_ShouldSurfaceAnUpsertFailure(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	db := newTestUserDB(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), db, provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	provider.nextID = provider.signIDToken(t, "sub-alice", "client-1", "nonce-1", map[string]any{"preferred_username": "alice"})

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get underlying sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("failed to close the database: %v", err)
	}

	if _, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier()); err == nil {
		t.Error("HandleCallback() error = nil, want error when persisting the user fails")
	}
}

func TestHandleCallback_ShouldRejectANonceMismatch(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	provider.nextID = provider.signIDToken(t, "sub-alice", "client-1", "nonce-issued", map[string]any{
		"preferred_username": "alice",
	})

	if _, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-expected", oauth2.GenerateVerifier()); err == nil {
		t.Error("HandleCallback() error = nil, want an error for a nonce mismatch")
	}
}

func TestHandleCallback_ShouldRejectATokenMissingTheUsernameClaim(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	// No preferred_username claim at all.
	provider.nextID = provider.signIDToken(t, "sub-alice", "client-1", "nonce-1", nil)

	if _, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier()); err == nil {
		t.Error("HandleCallback() error = nil, want an error for a missing username claim")
	}
}

func TestHandleCallback_ShouldFallBackToTheStandardEmailClaim(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	c := newTestAuthConfig(provider, "client-1")
	c.AuthConfig.Fields.Email = "" // unconfigured: falls back to "email"
	svc, err := NewAuthService(context.Background(), c, newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	provider.nextID = provider.signIDToken(t, "sub-bob", "client-1", "nonce-1", map[string]any{
		"preferred_username": "bob",
		"email":              "bob@example.com",
	})

	identity, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier())
	if err != nil {
		t.Fatalf("HandleCallback() error = %v", err)
	}
	if identity.Email != "bob@example.com" {
		t.Errorf("got Email %q, want %q", identity.Email, "bob@example.com")
	}
}

// newTestAuthConfigNoPKCE is newTestAuthConfig with authentication.disable_pkce
// set, for the provider that refuses a code challenge.
func newTestAuthConfigNoPKCE(provider *fakeOIDCProvider, clientID string) *config.Config {
	c := newTestAuthConfig(provider, clientID)
	c.AuthConfig.DisablePKCE = true
	return c
}

// With PKCE disabled the authorization request carries no code_challenge at
// all: a provider that refuses the parameter refuses it however it is
// spelled, so an empty one would be no better than the challenge itself.
func TestAuthorizationURL_ShouldOmitTheCodeChallengeWhenPKCEIsDisabled(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfigNoPKCE(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	authURL, nonce, pkceVerifier, err := svc.AuthorizationURL(context.Background(), "state-1")
	if err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}
	if pkceVerifier != "" {
		t.Errorf("AuthorizationURL() returned verifier %q, want an empty one", pkceVerifier)
	}
	if strings.Contains(authURL, "code_challenge") {
		t.Errorf("authURL = %q, want no code_challenge parameter", authURL)
	}
	if !strings.Contains(authURL, "nonce="+nonce) {
		t.Errorf("authURL = %q, want the nonce to survive disabling PKCE", authURL)
	}
}

// The claims echo takes the same switch, and keeps prompt=login either way.
func TestEchoAuthorizationURL_ShouldOmitTheCodeChallengeWhenPKCEIsDisabled(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfigNoPKCE(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	authURL, _, pkceVerifier, err := svc.EchoAuthorizationURL(context.Background(), "state-1")
	if err != nil {
		t.Fatalf("EchoAuthorizationURL() error = %v", err)
	}
	if pkceVerifier != "" {
		t.Errorf("EchoAuthorizationURL() returned verifier %q, want an empty one", pkceVerifier)
	}
	if strings.Contains(authURL, "code_challenge") {
		t.Errorf("authURL = %q, want no code_challenge parameter", authURL)
	}
	if !strings.Contains(authURL, "prompt=login") {
		t.Errorf("authURL = %q, want prompt=login", authURL)
	}
}

// An exchange for a login that issued no verifier sends no code_verifier:
// an empty one is a malformed parameter rather than an absent one.
func TestHandleCallback_ShouldOmitTheCodeVerifierWhenNoneWasIssued(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfigNoPKCE(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	provider.nextID = provider.signIDToken(t, "sub-alice", "client-1", "nonce-1", map[string]any{
		"preferred_username": "alice",
	})

	if _, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", ""); err != nil {
		t.Fatalf("HandleCallback() error = %v", err)
	}
	if _, ok := provider.tokenForm["code_verifier"]; ok {
		t.Errorf("token request sent code_verifier=%q, want the parameter left out entirely", provider.tokenForm.Get("code_verifier"))
	}
}

// And the default path still sends it, so the switch is what decides and
// not an accident of the exchange.
func TestHandleCallback_ShouldSendTheCodeVerifierWhenOneWasIssued(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t)
	svc, err := NewAuthService(context.Background(), newTestAuthConfig(provider, "client-1"), newTestUserDB(t), provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}

	provider.nextID = provider.signIDToken(t, "sub-alice", "client-1", "nonce-1", map[string]any{
		"preferred_username": "alice",
	})

	verifier := oauth2.GenerateVerifier()
	if _, err := svc.HandleCallback(context.Background(), "auth-code", "nonce-1", verifier); err != nil {
		t.Fatalf("HandleCallback() error = %v", err)
	}
	if got := provider.tokenForm.Get("code_verifier"); got != verifier {
		t.Errorf("token request sent code_verifier=%q, want %q", got, verifier)
	}
}
