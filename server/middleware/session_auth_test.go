package middleware

// Test methodology: a real gin-contrib/sessions cookie store, matching
// bootstrap.initRouter, driven with httptest.NewRecorder(). Each Set*/Pop*
// helper is exercised directly against a throwaway route rather than
// through the full auth handler flow (that's server/controller's job) —
// these tests are about the session read/write primitives themselves.

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/service"
)

func newSessionTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))
	return r
}

// doSessionRequest runs handler as a route on a fresh session-backed
// router, carrying prior's cookie forward if given, and returns the
// recorder.
func doSessionRequest(t *testing.T, handler gin.HandlerFunc, prior *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()

	r := newSessionTestRouter()
	r.GET("/probe", handler)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	if prior != nil {
		for _, c := range prior.Result().Cookies() {
			req.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSetAndPopOIDCState(t *testing.T) {
	t.Parallel()

	setResp := doSessionRequest(t, func(c *gin.Context) {
		if err := SetOIDCState(c, "state-1"); err != nil {
			t.Fatalf("SetOIDCState() error = %v", err)
		}
	}, nil)

	var got string
	popResp := doSessionRequest(t, func(c *gin.Context) {
		var err error
		got, err = PopOIDCState(c)
		if err != nil {
			t.Fatalf("PopOIDCState() error = %v", err)
		}
	}, setResp)

	if got != "state-1" {
		t.Errorf("PopOIDCState() = %q, want %q", got, "state-1")
	}

	// Popped means gone: a second pop against the same (now-cleared)
	// session must come back empty.
	var second string
	doSessionRequest(t, func(c *gin.Context) {
		var err error
		second, err = PopOIDCState(c)
		if err != nil {
			t.Fatalf("PopOIDCState() error = %v", err)
		}
	}, popResp)
	if second != "" {
		t.Errorf("second PopOIDCState() = %q, want empty (already consumed)", second)
	}
}

func TestPopOIDCState_ShouldReturnEmptyWhenNeverSet(t *testing.T) {
	t.Parallel()

	var got string
	doSessionRequest(t, func(c *gin.Context) {
		var err error
		got, err = PopOIDCState(c)
		if err != nil {
			t.Fatalf("PopOIDCState() error = %v", err)
		}
	}, nil)

	if got != "" {
		t.Errorf("PopOIDCState() = %q, want empty", got)
	}
}

func TestSetAndPopOIDCNonce(t *testing.T) {
	t.Parallel()

	setResp := doSessionRequest(t, func(c *gin.Context) {
		if err := SetOIDCNonce(c, "nonce-1"); err != nil {
			t.Fatalf("SetOIDCNonce() error = %v", err)
		}
	}, nil)

	var got string
	doSessionRequest(t, func(c *gin.Context) {
		var err error
		got, err = PopOIDCNonce(c)
		if err != nil {
			t.Fatalf("PopOIDCNonce() error = %v", err)
		}
	}, setResp)

	if got != "nonce-1" {
		t.Errorf("PopOIDCNonce() = %q, want %q", got, "nonce-1")
	}
}

func TestSetAndPopReturnURL(t *testing.T) {
	t.Parallel()

	setResp := doSessionRequest(t, func(c *gin.Context) {
		if err := SetReturnURL(c, "/approve/req-1"); err != nil {
			t.Fatalf("SetReturnURL() error = %v", err)
		}
	}, nil)

	var got string
	doSessionRequest(t, func(c *gin.Context) {
		var err error
		got, err = PopReturnURL(c)
		if err != nil {
			t.Fatalf("PopReturnURL() error = %v", err)
		}
	}, setResp)

	if got != "/approve/req-1" {
		t.Errorf("PopReturnURL() = %q, want %q", got, "/approve/req-1")
	}
}

func TestPopReturnURL_ShouldReturnEmptyWhenNeverSet(t *testing.T) {
	t.Parallel()

	var got string
	doSessionRequest(t, func(c *gin.Context) {
		var err error
		got, err = PopReturnURL(c)
		if err != nil {
			t.Fatalf("PopReturnURL() error = %v", err)
		}
	}, nil)

	if got != "" {
		t.Errorf("PopReturnURL() = %q, want empty", got)
	}
}

// TestSetIdentitySessionAndSessionAuthMiddleware asserts what a
// service.Identity keeps across the session round trip, compared as a
// struct rather than field by field on purpose. A field-by-field check
// silently ignores any field added to Identity afterwards, which is how
// OtherAccounts and ServiceAccounts came to be dropped by the middleware
// with every test still green: nothing here mentioned them, so nothing
// noticed.
//
// Every field the session carries is populated, so a dropped one shows up
// as a difference. The exception is Extra, which the session deliberately
// does not carry: it is a map of operator-defined claim values rather than
// a flat list, and the one path that needs it rehydrates it from the
// approver's users row instead (service.CertRequestService.Approve,
// "identity.Extra = decodeExtraFields"). identityFieldCount below makes a
// newly added field impossible to leave unasserted.
func TestSetIdentitySessionAndSessionAuthMiddleware(t *testing.T) {
	t.Parallel()

	identity := &service.Identity{
		Subject:         "sub-alice",
		Username:        "alice",
		Email:           "alice@example.com",
		Groups:          []string{"ssh-users", "admins"},
		OtherAccounts:   []string{"alice-admin", "alice-ops"},
		ServiceAccounts: []string{"svc-deploy", "svc-backup"},
		// Extra is left nil: its element type is unexported, so this
		// package cannot build one, and the middleware never sets it
		// either. The assertion below still pins that it stays nil.
	}

	setResp := doSessionRequest(t, func(c *gin.Context) {
		if err := SetIdentitySession(c, identity); err != nil {
			t.Fatalf("SetIdentitySession() error = %v", err)
		}
	}, nil)

	r := newSessionTestRouter()
	var gotIdentity *service.Identity
	var gotOK bool
	r.GET("/whoami", NewSessionAuthMiddleware(5*time.Minute, time.Hour).Add(), func(c *gin.Context) {
		gotIdentity, gotOK = Identity(c)
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	for _, c := range setResp.Result().Cookies() {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if !gotOK {
		t.Fatal("expected Identity() to find an identity on the context")
	}
	if !reflect.DeepEqual(gotIdentity, identity) {
		t.Errorf("round-tripped identity = %+v, want %+v", gotIdentity, identity)
	}
	if gotIdentity.Extra != nil {
		t.Errorf("round-tripped Extra = %v, want nil: the session does not carry it", gotIdentity.Extra)
	}
}

// identityFieldCount is the number of service.Identity fields the test
// above accounts for. It exists so that adding a field to Identity fails
// here rather than silently riding along unasserted: populate the new
// field in that test, put it on the carried or the deliberately-excluded
// side, and bump this. It has already earned its keep once, catching
// Identity.Extra arriving from another branch.
const identityFieldCount = 7

func TestIdentity_ShouldAccountForEveryFieldInTheRoundTripTest(t *testing.T) {
	t.Parallel()

	if got := reflect.TypeOf(service.Identity{}).NumField(); got != identityFieldCount {
		t.Errorf("service.Identity has %d fields, want %d — a field was added or removed; "+
			"decide whether the session carries it, update "+
			"TestSetIdentitySessionAndSessionAuthMiddleware, and change identityFieldCount",
			got, identityFieldCount)
	}
}

// slidingExpiryRequest logs an identity in, then replays the session cookie
// against a route guarded by a middleware built with the given idle window
// and absolute cap, returning the second response's status code and whether
// it reissued the session cookie.
func slidingExpiryRequest(t *testing.T, idleTimeout, maxSession time.Duration) (int, bool) {
	t.Helper()

	setResp := doSessionRequest(t, func(c *gin.Context) {
		if err := SetIdentitySession(c, &service.Identity{Subject: "sub-alice"}); err != nil {
			t.Fatalf("SetIdentitySession() error = %v", err)
		}
	}, nil)

	r := newSessionTestRouter()
	// The error handler is what turns the middleware's UnauthorizedError
	// into a 401 status; without it a rejection reads as an empty 200.
	r.Use(NewErrorHandlerMiddleware().Add())
	r.GET("/whoami", NewSessionAuthMiddleware(idleTimeout, maxSession).Add(), func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	for _, c := range setResp.Result().Cookies() {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w.Code, len(w.Result().Cookies()) > 0
}

func TestSessionAuthMiddleware_ShouldReissueCookieWhenPastHalfIdleWindow(t *testing.T) {
	t.Parallel()

	// A negative idle window makes the half-life threshold negative, so a
	// freshly-issued session is already "past" it — the refresh must fire
	// without the test having to manipulate clocks. The cap stays generous
	// so only the sliding path is in play.
	status, reissued := slidingExpiryRequest(t, -time.Minute, time.Hour)
	if status != http.StatusOK {
		t.Fatalf("got status %d, want %d", status, http.StatusOK)
	}
	if !reissued {
		t.Fatal("expected a session past its idle half-life to be re-saved with a fresh cookie")
	}
}

func TestSessionAuthMiddleware_ShouldNotReissueCookieWhenFresh(t *testing.T) {
	t.Parallel()

	// A generous idle window keeps the just-issued session well inside its
	// half-life, so the middleware must leave the cookie alone — refreshing
	// every request is exactly what the half-life check exists to avoid.
	status, reissued := slidingExpiryRequest(t, time.Hour, 2*time.Hour)
	if status != http.StatusOK {
		t.Fatalf("got status %d, want %d", status, http.StatusOK)
	}
	if reissued {
		t.Fatal("expected a fresh session to not be re-saved on every request")
	}
}

func TestSessionAuthMiddleware_ShouldRejectSessionPastAbsoluteCap(t *testing.T) {
	t.Parallel()

	// A negative cap puts even a just-issued session past its absolute
	// lifetime, so the request must come back unauthorized no matter how
	// generous the idle window is — activity never extends the cap.
	status, _ := slidingExpiryRequest(t, time.Hour, -time.Minute)
	if status != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d for a session past its absolute cap", status, http.StatusUnauthorized)
	}
}

// TestSessionAuthMiddleware_ShouldFailClosedWithoutASession covers the
// no-subject guard directly, independent of server/controller's own
// end-to-end coverage of the same behavior.
func TestSessionAuthMiddleware_ShouldFailClosedWithoutASession(t *testing.T) {
	t.Parallel()

	r := newSessionTestRouter()
	r.Use(NewErrorHandlerMiddleware().Add())
	var reached bool
	r.GET("/whoami", NewSessionAuthMiddleware(5*time.Minute, time.Hour).Add(), func(c *gin.Context) {
		reached = true
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/whoami", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if reached {
		t.Error("expected the handler to never run without a session identity")
	}
}

// TestSessionAuthMiddleware_ShouldHandleNoGroups covers a session with an
// identity but empty list claims — a real scenario for a user in no groups
// with no linked accounts. Each must come back nil rather than the
// one-element slice strings.Split gives for "".
func TestSessionAuthMiddleware_ShouldHandleNoGroups(t *testing.T) {
	t.Parallel()

	setResp := doSessionRequest(t, func(c *gin.Context) {
		if err := SetIdentitySession(c, &service.Identity{Subject: "sub-bob", Username: "bob"}); err != nil {
			t.Fatalf("SetIdentitySession() error = %v", err)
		}
	}, nil)

	r := newSessionTestRouter()
	var gotIdentity *service.Identity
	r.GET("/whoami", NewSessionAuthMiddleware(5*time.Minute, time.Hour).Add(), func(c *gin.Context) {
		gotIdentity, _ = Identity(c)
	})
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	for _, c := range setResp.Result().Cookies() {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if len(gotIdentity.Groups) != 0 {
		t.Errorf("got Groups %v, want empty", gotIdentity.Groups)
	}
	if len(gotIdentity.OtherAccounts) != 0 {
		t.Errorf("got OtherAccounts %v, want empty", gotIdentity.OtherAccounts)
	}
	if len(gotIdentity.ServiceAccounts) != 0 {
		t.Errorf("got ServiceAccounts %v, want empty", gotIdentity.ServiceAccounts)
	}
}

func TestClearIdentitySession(t *testing.T) {
	t.Parallel()

	identity := &service.Identity{Subject: "sub-alice", Username: "alice"}
	setResp := doSessionRequest(t, func(c *gin.Context) {
		if err := SetIdentitySession(c, identity); err != nil {
			t.Fatalf("SetIdentitySession() error = %v", err)
		}
	}, nil)

	clearResp := doSessionRequest(t, func(c *gin.Context) {
		if err := ClearIdentitySession(c); err != nil {
			t.Fatalf("ClearIdentitySession() error = %v", err)
		}
	}, setResp)

	r := newSessionTestRouter()
	r.Use(NewErrorHandlerMiddleware().Add())
	r.GET("/whoami", NewSessionAuthMiddleware(5*time.Minute, time.Hour).Add(), func(c *gin.Context) {
		c.String(http.StatusOK, "should not reach here")
	})
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	for _, c := range clearResp.Result().Cookies() {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d — the session should be cleared", w.Code, http.StatusUnauthorized)
	}
}

func TestIdentity_ShouldReportFalseWhenNothingIsSet(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	var ok bool
	r.GET("/probe", func(c *gin.Context) {
		_, ok = Identity(c)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))

	if ok {
		t.Error("expected Identity() to report false with nothing on the context")
	}
}

// TestIdentity_ShouldReportFalseForTheWrongType guards the type assertion:
// a routing mistake that puts something else under IdentityContextKey must
// not panic MustGet-style, and must not be silently treated as a valid
// identity either.
func TestIdentity_ShouldReportFalseForTheWrongType(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	var ok bool
	r.GET("/probe", func(c *gin.Context) {
		c.Set(IdentityContextKey, "not an identity")
		_, ok = Identity(c)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))

	if ok {
		t.Error("expected Identity() to report false for a value of the wrong type")
	}
}
