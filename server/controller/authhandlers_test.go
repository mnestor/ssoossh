package controller

// Test methodology: real gin-contrib/sessions cookie store (matching
// bootstrap.initRouter and webapi_test.go's TestWebReadEndpoints), driven
// with httptest.NewRecorder() but carrying the Set-Cookie header forward
// between requests by hand — that's what makes login -> callback a genuine
// two-request round trip through the same session, the way a browser does
// it, without needing a real listener.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
)

// fakeAuthService is a test double for service.AuthProvider.
type fakeAuthService struct {
	authURL         string
	pkceVerifier    string
	nonce           string
	authURLErr      error
	identity        *service.Identity
	handleCbErr     error
	gotCode         string
	gotCallbackNonc string
	gotPKCEVerifier string
}

func (f *fakeAuthService) AuthorizationURL(_ context.Context, _ string) (string, string, string, error) {
	if f.authURLErr != nil {
		return "", "", "", f.authURLErr
	}
	return f.authURL, f.nonce, f.pkceVerifier, nil
}

func (f *fakeAuthService) HandleCallback(_ context.Context, code, nonce, pkceVerifier string) (*service.Identity, error) {
	f.gotCode = code
	f.gotCallbackNonc = nonce
	f.gotPKCEVerifier = pkceVerifier
	if f.handleCbErr != nil {
		return nil, f.handleCbErr
	}
	return f.identity, nil
}

// newAuthTestRouter wires a real cookie-backed session store and
// NewAuthController, matching bootstrap.initRouter's setup.
func newAuthTestRouter(authSvc service.AuthProvider, csrf gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	r.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))
	NewAuthController(&r.RouterGroup, authSvc, csrf)
	return r
}

// doRequest issues req against r, carrying any cookies from prior into the
// request first — the hand-rolled equivalent of a browser's cookie jar.
//
// A single handler can call sess.Save() more than once (loginHandler saves
// once per Set*, callbackHandler once per Pop*/Set*), and each call emits
// its own Set-Cookie header for the cumulative session at that point —
// http.Response.Cookies() returns all of them, in write order, under the
// same name. A real browser keeps only the last one it saw for a given
// name; deduping here does the same, keyed by cookie name.
func doRequest(r *gin.Engine, req *http.Request, prior *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	if prior != nil {
		latest := make(map[string]*http.Cookie)
		for _, c := range prior.Result().Cookies() {
			latest[c.Name] = c
		}
		for _, c := range latest {
			req.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLoginHandler_ShouldRedirectToTheAuthorizationURL(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{authURL: "https://idp.example.com/authorize?state=abc", nonce: "n-1"}
	r := newAuthTestRouter(svc, passthrough)

	w := doRequest(r, httptest.NewRequest(http.MethodGet, "/login", nil), nil)

	if w.Code != http.StatusFound {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusFound, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != svc.authURL {
		t.Errorf("got Location %q, want %q", got, svc.authURL)
	}
}

func TestLoginHandler_ShouldSurfaceAnAuthorizationURLError(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{authURLErr: errors.New("provider unreachable")}
	r := newAuthTestRouter(svc, passthrough)

	w := doRequest(r, httptest.NewRequest(http.MethodGet, "/login", nil), nil)

	if w.Code == http.StatusFound {
		t.Fatalf("expected an error response, got a redirect to %q", w.Header().Get("Location"))
	}
}

// TestLoginThenCallback_ShouldEstablishASessionAndRedirectToReturnTo covers
// the full round trip: login captures state/nonce/return_to in the session,
// callback checks state, exchanges the code, and redirects back to
// return_to with the identity now in the session.
func TestLoginThenCallback_ShouldEstablishASessionAndRedirectToReturnTo(t *testing.T) {
	t.Parallel()

	identity := &service.Identity{Subject: "sub-alice", Username: "alice", Email: "alice@example.com"}
	svc := &fakeAuthService{authURL: "https://idp.example.com/authorize", nonce: "n-1", identity: identity}
	r := newAuthTestRouter(svc, passthrough)

	loginReq := httptest.NewRequest(http.MethodGet, "/login?return_to=/approve/req-1", nil)
	loginResp := doRequest(r, loginReq, nil)
	if loginResp.Code != http.StatusFound {
		t.Fatalf("login: got status %d, want %d", loginResp.Code, http.StatusFound)
	}

	// The real state value lives only in the session cookie; the OIDC
	// provider it's meant to defend against never sees it, so a real client
	// reads it back off the redirect it was sent to. Standing in for that
	// here as the "abc" fixture would only prove the mock, not the flow.
	state, err := extractSetOIDCState(r, loginResp)
	if err != nil {
		t.Fatalf("failed to recover the state value from the session: %v", err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/callback?code=auth-code-1&state="+state, nil)
	callbackResp := doRequest(r, callbackReq, loginResp)

	if callbackResp.Code != http.StatusFound {
		t.Fatalf("callback: got status %d, want %d, body: %s", callbackResp.Code, http.StatusFound, callbackResp.Body.String())
	}
	if got := callbackResp.Header().Get("Location"); got != "/approve/req-1" {
		t.Errorf("got Location %q, want %q", got, "/approve/req-1")
	}
	if svc.gotCode != "auth-code-1" {
		t.Errorf("HandleCallback got code %q, want %q", svc.gotCode, "auth-code-1")
	}
	if svc.gotCallbackNonc != "n-1" {
		t.Errorf("HandleCallback got nonce %q, want %q", svc.gotCallbackNonc, "n-1")
	}

	// The session now carries the identity: a follow-up authenticated
	// request should see it.
	meRouter := gin.New()
	meRouter.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))
	meRouter.GET("/whoami", middleware.NewSessionAuthMiddleware(5*time.Minute, time.Hour).Add(), func(c *gin.Context) {
		id, ok := middleware.Identity(c)
		if !ok {
			t.Fatal("expected an identity on the context")
		}
		c.String(http.StatusOK, id.Subject)
	})
	meResp := doRequest(meRouter, httptest.NewRequest(http.MethodGet, "/whoami", nil), callbackResp)
	if meResp.Code != http.StatusOK || meResp.Body.String() != "sub-alice" {
		t.Errorf("got status %d body %q, want 200 %q", meResp.Code, meResp.Body.String(), "sub-alice")
	}
}

// extractSetOIDCState replays the same session cookie through a throwaway
// handler that reads sessionKeyOIDCState back out via PopOIDCState, so the
// test recovers the real value SetOIDCState wrote rather than assuming it.
func extractSetOIDCState(_ *gin.Engine, loginResp *httptest.ResponseRecorder) (string, error) {
	probe := gin.New()
	probe.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))
	var state string
	var popErr error
	probe.GET("/probe", func(c *gin.Context) {
		state, popErr = middleware.PopOIDCState(c)
	})
	doRequest(probe, httptest.NewRequest(http.MethodGet, "/probe", nil), loginResp)
	return state, popErr
}

func TestCallbackHandler_ShouldRejectAStateMismatch(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{authURL: "https://idp.example.com/authorize", nonce: "n-1"}
	r := newAuthTestRouter(svc, passthrough)

	loginResp := doRequest(r, httptest.NewRequest(http.MethodGet, "/login", nil), nil)

	callbackReq := httptest.NewRequest(http.MethodGet, "/callback?code=auth-code-1&state=wrong-state", nil)
	callbackResp := doRequest(r, callbackReq, loginResp)

	if callbackResp.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d, body: %s", callbackResp.Code, http.StatusUnauthorized, callbackResp.Body.String())
	}
}

func TestCallbackHandler_ShouldRejectAMissingState(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{}
	r := newAuthTestRouter(svc, passthrough)

	// No prior /login call: nothing was ever stored, so PopOIDCState
	// returns "" and any presented state must be rejected.
	callbackReq := httptest.NewRequest(http.MethodGet, "/callback?code=auth-code-1&state=", nil)
	callbackResp := doRequest(r, callbackReq, nil)

	if callbackResp.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d, body: %s", callbackResp.Code, http.StatusUnauthorized, callbackResp.Body.String())
	}
}

func TestCallbackHandler_ShouldSurfaceAHandleCallbackError(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{authURL: "https://idp.example.com/authorize", nonce: "n-1", handleCbErr: errors.New("token exchange failed")}
	r := newAuthTestRouter(svc, passthrough)

	loginResp := doRequest(r, httptest.NewRequest(http.MethodGet, "/login", nil), nil)
	state, err := extractSetOIDCState(r, loginResp)
	if err != nil {
		t.Fatalf("failed to recover state: %v", err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/callback?code=auth-code-1&state="+state, nil)
	callbackResp := doRequest(r, callbackReq, loginResp)

	if callbackResp.Code == http.StatusFound {
		t.Fatalf("expected an error response, got a redirect to %q", callbackResp.Header().Get("Location"))
	}
}

// TestCallbackHandler_ShouldFallBackToRootWithoutAReturnURL covers a login
// that never set return_to (no query param, or an unsafe one that
// loginHandler already refused to store): the callback must not redirect
// nowhere, or trust a client-suppliable header — it falls back to "/".
func TestCallbackHandler_ShouldFallBackToRootWithoutAReturnURL(t *testing.T) {
	t.Parallel()

	identity := &service.Identity{Subject: "sub-alice"}
	svc := &fakeAuthService{authURL: "https://idp.example.com/authorize", nonce: "n-1", identity: identity}
	r := newAuthTestRouter(svc, passthrough)

	// No return_to query param at all.
	loginResp := doRequest(r, httptest.NewRequest(http.MethodGet, "/login", nil), nil)
	state, err := extractSetOIDCState(r, loginResp)
	if err != nil {
		t.Fatalf("failed to recover state: %v", err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/callback?code=auth-code-1&state="+state, nil)
	callbackResp := doRequest(r, callbackReq, loginResp)

	if callbackResp.Code != http.StatusFound {
		t.Fatalf("got status %d, want %d, body: %s", callbackResp.Code, http.StatusFound, callbackResp.Body.String())
	}
	if got := callbackResp.Header().Get("Location"); got != "/" {
		t.Errorf("got Location %q, want %q", got, "/")
	}
}

// TestLoginHandler_ShouldIgnoreAnUnsafeReturnTo covers loginHandler's own
// guard: an absolute or protocol-relative return_to must never make it into
// the session for callbackHandler to redirect to.
func TestLoginHandler_ShouldIgnoreAnUnsafeReturnTo(t *testing.T) {
	t.Parallel()

	identity := &service.Identity{Subject: "sub-alice"}
	svc := &fakeAuthService{authURL: "https://idp.example.com/authorize", nonce: "n-1", identity: identity}
	r := newAuthTestRouter(svc, passthrough)

	loginResp := doRequest(r, httptest.NewRequest(http.MethodGet, "/login?return_to=https://evil.example.com", nil), nil)
	state, err := extractSetOIDCState(r, loginResp)
	if err != nil {
		t.Fatalf("failed to recover state: %v", err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/callback?code=auth-code-1&state="+state, nil)
	callbackResp := doRequest(r, callbackReq, loginResp)

	if got := callbackResp.Header().Get("Location"); got != "/" {
		t.Errorf("an unsafe return_to must never survive to the callback redirect: got Location %q, want %q", got, "/")
	}
}

func TestLogoutHandler_ShouldClearTheSessionAndReturnLoggedOut(t *testing.T) {
	t.Parallel()

	identity := &service.Identity{Subject: "sub-alice"}
	svc := &fakeAuthService{authURL: "https://idp.example.com/authorize", nonce: "n-1", identity: identity}
	r := newAuthTestRouter(svc, passthrough)

	loginResp := doRequest(r, httptest.NewRequest(http.MethodGet, "/login", nil), nil)
	state, err := extractSetOIDCState(r, loginResp)
	if err != nil {
		t.Fatalf("failed to recover state: %v", err)
	}
	callbackResp := doRequest(r, httptest.NewRequest(http.MethodGet, "/callback?code=c&state="+state, nil), loginResp)

	logoutResp := doRequest(r, httptest.NewRequest(http.MethodPost, "/logout", nil), callbackResp)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", logoutResp.Code, http.StatusOK, logoutResp.Body.String())
	}
	if got := logoutResp.Body.String(); got == "" {
		t.Error("expected a response body")
	}

	// The session must actually be gone: a follow-up authenticated request
	// with the post-logout cookie must fail closed.
	meRouter := gin.New()
	meRouter.Use(middleware.NewErrorHandlerMiddleware().Add())
	meRouter.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))
	meRouter.GET("/whoami", middleware.NewSessionAuthMiddleware(5*time.Minute, time.Hour).Add(), func(c *gin.Context) {
		c.String(http.StatusOK, "should not reach here")
	})
	meResp := doRequest(meRouter, httptest.NewRequest(http.MethodGet, "/whoami", nil), logoutResp)
	if meResp.Code == http.StatusOK {
		t.Error("expected the session to be cleared; a post-logout request still authenticated")
	}
}

// oversizedSessionSeed is a gin.HandlerFunc that stuffs enough junk into the
// session to push gorilla/securecookie's encoded cookie past its 4096-byte
// limit, so the very next sess.Save() in the chain — the first one each
// handler under test makes — fails with a real, injectable error. Inserted
// ahead of the auth routes, it's how these tests reach the session-write
// error branches without a fake Store: cookie sessions have no such
// interface to substitute, but oversizing genuinely breaks Save().
func oversizedSessionSeed(c *gin.Context) {
	sess := sessions.Default(c)
	sess.Set("junk", strings.Repeat("x", 8192))
	_ = sess.Save()
	c.Next()
}

func TestLoginHandler_ShouldSurfaceASessionWriteError(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{authURL: "https://idp.example.com/authorize", nonce: "n-1"}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	r.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))
	r.Use(oversizedSessionSeed)
	NewAuthController(&r.RouterGroup, svc, passthrough)

	w := doRequest(r, httptest.NewRequest(http.MethodGet, "/login", nil), nil)

	if w.Code == http.StatusFound {
		t.Fatalf("expected an error response when the session fails to save, got a redirect to %q", w.Header().Get("Location"))
	}
}

func TestCallbackHandler_ShouldSurfaceASessionWriteError(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	r.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))
	r.Use(oversizedSessionSeed)
	NewAuthController(&r.RouterGroup, svc, passthrough)

	w := doRequest(r, httptest.NewRequest(http.MethodGet, "/callback?code=c&state=s", nil), nil)

	if w.Code == http.StatusFound {
		t.Fatalf("expected an error response when the session fails to save, got a redirect to %q", w.Header().Get("Location"))
	}
}

func TestRandomState_ShouldReturnDistinctURLSafeValues(t *testing.T) {
	t.Parallel()

	a, err := randomState()
	if err != nil {
		t.Fatalf("randomState() error = %v", err)
	}
	b, err := randomState()
	if err != nil {
		t.Fatalf("randomState() error = %v", err)
	}
	if a == "" || b == "" {
		t.Fatal("randomState() returned an empty value")
	}
	if a == b {
		t.Error("expected two calls to randomState() to return distinct values")
	}
}
