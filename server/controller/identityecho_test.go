package controller

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// echoTestRouter wires the echo start endpoint and the shared OIDC callback
// over a real session store, which is what the flow's two halves talk
// through.
func echoTestRouter(t *testing.T, authSvc service.AuthProvider, identity *service.Identity) *gin.Engine {
	t.Helper()
	return echoTestRouterWithCodes(t, authSvc, identity, nil)
}

// echoTestRouterWithCodes is echoTestRouter with the bootstrap echo's code
// store wired, for the redeem endpoint and the callback's record of it.
func echoTestRouterWithCodes(t *testing.T, authSvc service.AuthProvider, identity *service.Identity, codes service.IdentityEchoCodes) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.Admin.RequireGroup = "ssh-admins"

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	r.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))

	authGroup := r.Group("/auth")
	NewAuthController(authGroup, authSvc, codes, func(c *gin.Context) { c.Next() }, cfg)
	NewIdentityEchoController(
		&r.RouterGroup,
		authSvc,
		codes,
		identityMiddleware(identity),
		middleware.NewAdminAuthMiddleware(cfg).Add(),
		func(c *gin.Context) { c.Next() },
	)
	return r
}

// decodeEchoFragment pulls the payload back out of a redirect's fragment.
func decodeEchoFragment(t *testing.T, location string) webtypes.IdentityEchoPayload {
	t.Helper()

	_, fragment, found := strings.Cut(location, "#")
	if !found {
		t.Fatalf("redirect %q carries no fragment", location)
	}
	unescaped, err := url.PathUnescape(fragment)
	if err != nil {
		t.Fatalf("failed to unescape the fragment: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(unescaped)
	if err != nil {
		t.Fatalf("failed to base64-decode the fragment: %v", err)
	}
	var payload webtypes.IdentityEchoPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		t.Fatalf("failed to decode the echo payload: %v", err)
	}
	return payload
}

// TestIdentityEchoStart_ShouldRequireAdmin keeps the echo out of reach of
// the read-only roles: it drives a fresh authorization round trip and its
// annotated result is a configuration tool.
func TestIdentityEchoStart_ShouldRequireAdmin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		groups     []string
		wantStatus int
	}{
		{name: "an admin may start an echo", groups: []string{"ssh-admins"}, wantStatus: http.StatusOK},
		{name: "an ordinary user may not", groups: []string{"ssh-users"}, wantStatus: http.StatusForbidden},
		{name: "no groups at all may not", groups: nil, wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := &fakeAuthService{echoURL: "https://idp.example.com/authorize?prompt=login"}
			identity := &service.Identity{Subject: "sub-alice", Groups: tt.groups}
			r := echoTestRouter(t, svc, identity)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/identity/echo/start", nil))
			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestIdentityEcho_ShouldRenderClaimsWithoutTouchingTheSession is the whole
// contract: the callback returns the decoded token and establishes nothing.
func TestIdentityEcho_ShouldRenderClaimsWithoutTouchingTheSession(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{
		echoURL: "https://idp.example.com/authorize?prompt=login",
		nonce:   "nonce-1", pkceVerifier: "verifier-1",
		echoClaims: map[string]any{
			"sub":                "sub-alice",
			"preferred_username": "alice",
			"groups":             []any{"ssh-admins", "platform"},
			"employee_type":      "staff",
			"assurance_score":    float64(42),
		},
		mapping: service.ClaimMapping{
			Username: "preferred_username",
			Groups:   "groups",
			Email:    "email",
		},
		// A login through this fake would set a session; the echo must not
		// reach it.
		identity: &service.Identity{Subject: "sub-should-not-be-set", Username: "nobody"},
	}
	identity := &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}}
	r := echoTestRouter(t, svc, identity)

	start := httptest.NewRecorder()
	r.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/admin/identity/echo/start", nil))
	if start.Code != http.StatusOK {
		t.Fatalf("start: got status %d, want 200: %s", start.Code, start.Body.String())
	}
	if !svc.echoStarted {
		t.Error("the start endpoint did not ask for an echo authorization URL")
	}

	var startResp webtypes.IdentityEchoStartResponse
	decodeEnvelope(t, start.Body.Bytes(), &startResp)
	if !strings.Contains(startResp.AuthorizationURL, "prompt=login") {
		t.Errorf("authorization url = %q, want prompt=login", startResp.AuthorizationURL)
	}

	// Come back through the shared callback carrying the session cookie.
	state := oidcStateFromSession(t, r, start)
	cb := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(state), nil)
	for _, c := range start.Result().Cookies() {
		cb.AddCookie(c)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cb)

	if w.Code != http.StatusFound {
		t.Fatalf("callback: got status %d, want 302: %s", w.Code, w.Body.String())
	}
	if !svc.echoHandled {
		t.Fatal("the callback did not take the echo path")
	}
	if svc.gotCode != "abc" {
		t.Errorf("the echo callback got code %q, want abc", svc.gotCode)
	}

	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, echoResultPath+"#") {
		t.Fatalf("redirect = %q, want the echo page with a fragment", location)
	}
	// The claims must not be anywhere the server or a proxy would see
	// them, which is the whole reason for the fragment.
	if strings.Contains(strings.SplitN(location, "#", 2)[0], "employee_type") {
		t.Error("the echoed claims appeared in the redirect path rather than only the fragment")
	}

	payload := decodeEchoFragment(t, location)
	if payload.Claims["employee_type"] != "staff" {
		t.Errorf("echoed claims = %v, want the whole token", payload.Claims)
	}
	if payload.Mapping.Username != "preferred_username" {
		t.Errorf("mapping.username = %q, want the configured claim", payload.Mapping.Username)
	}
	if payload.IssuedAt.IsZero() {
		t.Error("the payload carries no issued_at, so a stale page cannot say so")
	}
}

// TestEchoRedirectURL_ShouldRoundTripThePayload pins the encoding both ends
// depend on.
func TestEchoRedirectURL_ShouldRoundTripThePayload(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	claims := map[string]any{"preferred_username": "o'brien", "groups": []any{"a/b", "c#d"}}

	report := service.BuildEchoReport(&service.EchoTokens{Claims: claims}, service.ClaimMapping{Username: "preferred_username"})
	got, err := echoRedirectURL(echoResultPath, report, now)
	if err != nil {
		t.Fatalf("echoRedirectURL() error = %v", err)
	}

	payload := decodeEchoFragment(t, got)
	if payload.Claims["preferred_username"] != "o'brien" {
		t.Errorf("round-tripped claims = %v, want the originals", payload.Claims)
	}
	if !payload.IssuedAt.Equal(now) {
		t.Errorf("round-tripped issued_at = %v, want %v", payload.IssuedAt, now)
	}
}

// oidcStateFromSession reads back the state the start endpoint stored, which
// the callback checks its query parameter against.
func oidcStateFromSession(t *testing.T, r *gin.Engine, from *httptest.ResponseRecorder) string {
	t.Helper()

	var state string
	r.GET("/test/oidc-state", func(c *gin.Context) {
		sess := sessions.Default(c)
		state, _ = sess.Get("oidc_state").(string)
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test/oidc-state", nil)
	for _, c := range from.Result().Cookies() {
		req.AddCookie(c)
	}
	r.ServeHTTP(httptest.NewRecorder(), req)

	if state == "" {
		t.Fatal("no OIDC state was stored by the echo start endpoint")
	}
	return state
}

// TestIdentityEcho_ShouldSendAFailedEchoBackToTheEchoPage covers the echo's
// failure path: a navigating browser shown a JSON error body had no way
// back, so the callback returns it to the echo page with the reason.
func TestIdentityEcho_ShouldSendAFailedEchoBackToTheEchoPage(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{
		echoURL: "https://idp.example.com/authorize?prompt=login",
		nonce:   "nonce-1", pkceVerifier: "verifier-1",
		echoCbErr: errors.New("token verification failed"),
	}
	identity := &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}}
	r := echoTestRouter(t, svc, identity)

	start := httptest.NewRecorder()
	r.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/admin/identity/echo/start", nil))
	state := oidcStateFromSession(t, r, start)
	cb := httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(state), nil)
	for _, c := range start.Result().Cookies() {
		cb.AddCookie(c)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cb)

	assertSignInFailure(t, w, echoResultPath, "failed")
}
