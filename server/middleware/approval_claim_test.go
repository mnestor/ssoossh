package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/service"
)

// Test methodology: table-driven over the (method, path, claimer outcome)
// combinations, against a fake service.ApprovalPageClaimer. The middleware's
// job is routing each outcome to pass-through, cookie, or redirect; the
// claim decision itself is the service's and is tested there.

// fakeClaimer is a test double for service.ApprovalPageClaimer, recording
// what it was asked and answering with a canned result.
type fakeClaimer struct {
	result service.ClaimPageResult
	err    error

	called       bool
	gotRequestID string
	gotToken     string
	gotUserAgent string
}

func (f *fakeClaimer) ClaimApprovalPage(_ context.Context, requestID, presentedToken, userAgent string) (service.ClaimPageResult, error) {
	f.called = true
	f.gotRequestID = requestID
	f.gotToken = presentedToken
	f.gotUserAgent = userAgent
	return f.result, f.err
}

// newClaimRouter builds a router with the middleware in front of a
// catch-all that records whether the page would have been served —
// standing in for the frontend's NoRoute handler.
func newClaimRouter(t *testing.T, claimer *fakeClaimer, secure bool) (*gin.Engine, *bool) {
	t.Helper()

	reached := false
	r := gin.New()
	// The error handler is part of the contract under test: a claim
	// failure aborts via c.Error, and this is what turns that into a 500.
	r.Use(NewErrorHandlerMiddleware().Add())
	r.Use(NewApprovalClaimMiddleware(claimer, secure).Add())
	r.NoRoute(func(c *gin.Context) {
		reached = true
		c.String(http.StatusOK, "spa")
	})
	return r, &reached
}

// claimCookie finds the claim cookie in resp, or nil.
func claimCookie(resp *http.Response) *http.Cookie {
	for _, ck := range resp.Cookies() {
		if ck.Name == claimCookieName {
			return ck
		}
	}
	return nil
}

func TestApprovalClaimMiddleware_ShouldRouteEachOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		method      string
		path        string
		result      service.ClaimPageResult
		wantStatus  int
		wantServed  bool
		wantClaimed bool // claimer consulted at all
		wantCookie  bool
		wantTarget  string // Location on a redirect
	}{
		{
			name:        "should claim and set the cookie on the first GET",
			method:      http.MethodGet,
			path:        "/approve/req-1",
			result:      service.ClaimPageResult{Outcome: service.ClaimPageClaimed, Token: "tok-1"},
			wantStatus:  http.StatusOK,
			wantServed:  true,
			wantClaimed: true,
			wantCookie:  true,
		},
		{
			name:        "should serve the page when the claiming cookie matches",
			method:      http.MethodGet,
			path:        "/approve/req-1",
			result:      service.ClaimPageResult{Outcome: service.ClaimPageMatched},
			wantStatus:  http.StatusOK,
			wantServed:  true,
			wantClaimed: true,
		},
		{
			name:        "should serve the page for an unknown request",
			method:      http.MethodGet,
			path:        "/approve/req-1",
			result:      service.ClaimPageResult{Outcome: service.ClaimPageUnknownRequest},
			wantStatus:  http.StatusOK,
			wantServed:  true,
			wantClaimed: true,
		},
		{
			name:        "should redirect a rejected client to the spent-link page",
			method:      http.MethodGet,
			path:        "/approve/req-1",
			result:      service.ClaimPageResult{Outcome: service.ClaimPageRejected},
			wantStatus:  http.StatusFound,
			wantClaimed: true,
			wantTarget:  "/approval-unavailable?reason=opened",
		},
		{
			name:        "should redirect a cookie-blocked browser to its own explanation",
			method:      http.MethodGet,
			path:        "/approve/req-1",
			result:      service.ClaimPageResult{Outcome: service.ClaimPageCookieBlocked},
			wantStatus:  http.StatusFound,
			wantClaimed: true,
			wantTarget:  "/approval-unavailable?reason=cookies",
		},
		{
			name:        "should claim on HEAD so scanner probes burn the link",
			method:      http.MethodHead,
			path:        "/approve/req-1",
			result:      service.ClaimPageResult{Outcome: service.ClaimPageClaimed, Token: "tok-1"},
			wantStatus:  http.StatusOK,
			wantServed:  true,
			wantClaimed: true,
			wantCookie:  true,
		},
		{
			name:       "should ignore a POST to the approval path",
			method:     http.MethodPost,
			path:       "/approve/req-1",
			wantStatus: http.StatusOK,
			wantServed: true,
		},
		{
			name:       "should ignore paths outside the approval page",
			method:     http.MethodGet,
			path:       "/dashboard",
			wantStatus: http.StatusOK,
			wantServed: true,
		},
		{
			name:       "should ignore the bare approval prefix",
			method:     http.MethodGet,
			path:       "/approve/",
			wantStatus: http.StatusOK,
			wantServed: true,
		},
		{
			name:       "should ignore deeper paths under the approval prefix",
			method:     http.MethodGet,
			path:       "/approve/req-1/extra",
			wantStatus: http.StatusOK,
			wantServed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			claimer := &fakeClaimer{result: tt.result}
			r, reached := newClaimRouter(t, claimer, true)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
			resp := w.Result()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("got status %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if *reached != tt.wantServed {
				t.Errorf("got page served %t, want %t", *reached, tt.wantServed)
			}
			if claimer.called != tt.wantClaimed {
				t.Errorf("got claimer consulted %t, want %t", claimer.called, tt.wantClaimed)
			}
			if got := claimCookie(resp) != nil; got != tt.wantCookie {
				t.Errorf("got claim cookie present %t, want %t", got, tt.wantCookie)
			}
			if tt.wantTarget != "" {
				if got := resp.Header.Get("Location"); got != tt.wantTarget {
					t.Errorf("got redirect target %q, want %q", got, tt.wantTarget)
				}
			}
		})
	}
}

// TestApprovalClaimMiddleware_ShouldSetTheCookieItWasToldTo pins the cookie
// attributes the proposal requires: path-scoped to the one request,
// HttpOnly, Secure when configured, and SameSite=Lax specifically so the
// top-level redirect back from the IdP still presents it.
func TestApprovalClaimMiddleware_ShouldSetTheCookieItWasToldTo(t *testing.T) {
	t.Parallel()

	claimer := &fakeClaimer{result: service.ClaimPageResult{Outcome: service.ClaimPageClaimed, Token: "tok-1"}}
	r, _ := newClaimRouter(t, claimer, true)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/approve/req-1", nil))

	ck := claimCookie(w.Result())
	if ck == nil {
		t.Fatal("expected the claim cookie to be set")
	}
	if ck.Value != "tok-1" {
		t.Errorf("got cookie value %q, want the minted token", ck.Value)
	}
	if ck.Path != "/approve/req-1" {
		t.Errorf("got cookie path %q, want it scoped to the one request", ck.Path)
	}
	if !ck.HttpOnly {
		t.Error("expected the cookie to be HttpOnly")
	}
	if !ck.Secure {
		t.Error("expected the cookie to be Secure when configured so")
	}
	if ck.SameSite != http.SameSiteLaxMode {
		t.Errorf("got SameSite %v, want Lax (Strict breaks the IdP return leg)", ck.SameSite)
	}
	if ck.MaxAge != claimCookieMaxAge {
		t.Errorf("got MaxAge %d, want %d", ck.MaxAge, claimCookieMaxAge)
	}
}

// TestApprovalClaimMiddleware_ShouldNotMarkTheCookieSecureOffTLS mirrors the
// session cookie's behavior: a Secure cookie over plain HTTP is silently
// dropped by the browser, which would force every visit into the
// cookie-blocked path.
func TestApprovalClaimMiddleware_ShouldNotMarkTheCookieSecureOffTLS(t *testing.T) {
	t.Parallel()

	claimer := &fakeClaimer{result: service.ClaimPageResult{Outcome: service.ClaimPageClaimed, Token: "tok-1"}}
	r, _ := newClaimRouter(t, claimer, false)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/approve/req-1", nil))

	ck := claimCookie(w.Result())
	if ck == nil {
		t.Fatal("expected the claim cookie to be set")
	}
	if ck.Secure {
		t.Error("expected the cookie not to be Secure when TLS is not in play")
	}
}

// TestApprovalClaimMiddleware_ShouldForwardWhatTheClientPresented pins the
// plumbing: the request ID from the path, the cookie value, and the user
// agent all reach the claimer as given.
func TestApprovalClaimMiddleware_ShouldForwardWhatTheClientPresented(t *testing.T) {
	t.Parallel()

	claimer := &fakeClaimer{result: service.ClaimPageResult{Outcome: service.ClaimPageMatched}}
	r, _ := newClaimRouter(t, claimer, true)

	req := httptest.NewRequest(http.MethodGet, "/approve/req-42", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (test)")
	req.AddCookie(&http.Cookie{Name: claimCookieName, Value: "tok-42"}) //nolint:gosec // G124: a request Cookie header carries no attributes; there is nothing to secure here.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if claimer.gotRequestID != "req-42" {
		t.Errorf("got request ID %q, want %q", claimer.gotRequestID, "req-42")
	}
	if claimer.gotToken != "tok-42" {
		t.Errorf("got presented token %q, want %q", claimer.gotToken, "tok-42")
	}
	if claimer.gotUserAgent != "Mozilla/5.0 (test)" {
		t.Errorf("got user agent %q, want %q", claimer.gotUserAgent, "Mozilla/5.0 (test)")
	}
}

// TestApprovalClaimMiddleware_ShouldFailClosedOnAClaimError refuses to
// serve the page unclaimed when the claim cannot be recorded: skipping the
// control silently would be worse than a rare 500.
func TestApprovalClaimMiddleware_ShouldFailClosedOnAClaimError(t *testing.T) {
	t.Parallel()

	claimer := &fakeClaimer{err: errors.New("database is down")}
	r, reached := newClaimRouter(t, claimer, true)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/approve/req-1", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if *reached {
		t.Error("expected the page not to be served when the claim failed")
	}
}
