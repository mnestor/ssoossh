package controller

import (
	"bytes"
	"context"
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
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
	"github.com/mnestor/ssoossh/test/testdb"
)

// bootstrapEcho is one bootstrap echo under test: a router with the real
// code store over a test database, and a live code minted in it.
type bootstrapEcho struct {
	router *gin.Engine
	db     *gorm.DB
	codes  *service.IdentityEchoCodeService
	minted *service.MintedEchoCode
}

// newBootstrapEcho wires the echo routes over svc and mints one code.
func newBootstrapEcho(t *testing.T, svc *fakeAuthService) *bootstrapEcho {
	t.Helper()
	db := testdb.New(t)
	codes := service.NewIdentityEchoCodeService(db)
	minted, err := codes.Mint(context.Background(), service.MintRequest{RequestedBy: "ssoossh@ssh-1"})
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	r := echoTestRouterWithCodes(t, svc, nil, codes)
	// The observed address must be the TCP peer: no proxy is trusted.
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies() error = %v", err)
	}
	return &bootstrapEcho{router: r, db: db, codes: codes, minted: minted}
}

// redeem posts code to the redeem endpoint from 203.0.113.9, with a
// forwarded header the server must not believe.
func (b *bootstrapEcho) redeem(code string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(webtypes.IdentityEchoRedeemRequest{Code: code})
	req := httptest.NewRequest(http.MethodPost, "/identity/echo/redeem", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	req.RemoteAddr = "203.0.113.9:40000"
	w := httptest.NewRecorder()
	b.router.ServeHTTP(w, req)
	return w
}

// roundTrip redeems the minted code and comes back through the callback
// with the session the redeem set, returning the callback's response and
// the cookies the browser then holds.
func (b *bootstrapEcho) roundTrip(t *testing.T, callbackQuery string) *httptest.ResponseRecorder {
	t.Helper()
	redeemed := b.redeem(b.minted.Code)
	if redeemed.Code != http.StatusOK {
		t.Fatalf("redeem: got status %d, want 200: %s", redeemed.Code, redeemed.Body.String())
	}
	state := oidcStateFromSession(t, b.router, redeemed)
	query := "state=" + url.QueryEscape(state)
	if callbackQuery != "" {
		query += "&" + callbackQuery
	}
	return doRequest(b.router, httptest.NewRequest(http.MethodGet, "/auth/callback?"+query, nil), redeemed)
}

// row reads the minted code's row back.
func (b *bootstrapEcho) row(t *testing.T) model.IdentityEchoCode {
	t.Helper()
	var row model.IdentityEchoCode
	if err := b.db.First(&row, "id = ?", b.minted.Row.ID).Error; err != nil {
		t.Fatalf("read the code row: %v", err)
	}
	return row
}

// decodeEchoFailure pulls the failure detail out of a redirect's fragment.
func decodeEchoFailure(t *testing.T, location string) webtypes.IdentityEchoFailure {
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
	var failure webtypes.IdentityEchoFailure
	if err := json.Unmarshal(decoded, &failure); err != nil {
		t.Fatalf("failed to decode the failure: %v", err)
	}
	return failure
}

func TestBootstrapEcho_ShouldShowTheClaimsWhenAValidCodeIsRedeemed(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{
		echoURL: "https://idp.example.com/authorize?prompt=login", nonce: "n-1",
		echoClaims: map[string]any{"sub": "sub-alice", "preferred_username": "alice", "groups": []any{"ssh-admins"}},
		mapping:    service.ClaimMapping{Subject: "sub", Username: "preferred_username", Groups: "groups"},
	}
	b := newBootstrapEcho(t, svc)

	w := b.roundTrip(t, "code=abc")

	location := w.Header().Get("Location")
	if !strings.HasPrefix(location, bootstrapEchoResultPath+"#") {
		t.Fatalf("redirect = %q, want the public echo page with a fragment", location)
	}
	payload := decodeEchoFragment(t, location)
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should carry the claims", ok: payload.Claims["preferred_username"] == "alice"},
		{name: "should say the sign-in would succeed", ok: payload.SignIn.Allowed},
		{name: "should offer the groups as admin group candidates", ok: len(payload.AdminGroupCandidates) == 1 && payload.AdminGroupCandidates[0] == "ssh-admins"},
		{name: "should record a clean outcome on the code", ok: b.row(t).CompletedAt != nil && b.row(t).Outcome == ""},
	}
	for _, tt := range tests {
		if !tt.ok {
			t.Errorf("%s: payload %+v", tt.name, payload)
		}
	}
}

func TestBootstrapEcho_ShouldShowTheClaimsWhenTheUsernameClaimIsMissing(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{
		echoURL: "https://idp.example.com/authorize", nonce: "n-1",
		echoClaims: map[string]any{"sub": "sub-alice", "upn": "alice@example.com"},
		mapping:    service.ClaimMapping{Subject: "sub", Username: "preferred_username"},
	}
	b := newBootstrapEcho(t, svc)

	payload := decodeEchoFragment(t, b.roundTrip(t, "code=abc").Header().Get("Location"))

	if payload.SignIn.Allowed || !strings.Contains(payload.SignIn.Reason, "preferred_username") || payload.Claims["upn"] != "alice@example.com" {
		t.Errorf("got sign_in %+v and claims %v, want the claims shown and the username failure named", payload.SignIn, payload.Claims)
	}
}

func TestIdentityEchoRedeem_ShouldRefuseAWrongUsedOrExpiredCodeTheSameWay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(t *testing.T, b *bootstrapEcho) string
	}{
		{name: "should refuse a wrong code", prepare: func(*testing.T, *bootstrapEcho) string { return "not-the-code" }},
		{name: "should refuse an empty code", prepare: func(*testing.T, *bootstrapEcho) string { return " " }},
		{name: "should refuse a used code", prepare: func(t *testing.T, b *bootstrapEcho) string {
			if w := b.redeem(b.minted.Code); w.Code != http.StatusOK {
				t.Fatalf("first redeem: got status %d", w.Code)
			}
			return b.minted.Code
		}},
		{name: "should refuse an expired code", prepare: func(t *testing.T, b *bootstrapEcho) string {
			if err := b.db.Model(&model.IdentityEchoCode{}).Where("id = ?", b.minted.Row.ID).
				Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
				t.Fatalf("expire the code: %v", err)
			}
			return b.minted.Code
		}},
	}
	var bodies []string
	for _, tt := range tests {
		b := newBootstrapEcho(t, &fakeAuthService{echoURL: "https://idp.example.com/authorize"})
		w := b.redeem(tt.prepare(t, b))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: got status %d, want 404", tt.name, w.Code)
		}
		bodies = append(bodies, w.Body.String())
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Errorf("refusals differ: %q and %q", bodies[0], bodies[i])
		}
	}
}

func TestIdentityEchoRedeem_ShouldRecordTheAddressTheServerObserved(t *testing.T) {
	t.Parallel()

	b := newBootstrapEcho(t, &fakeAuthService{echoURL: "https://idp.example.com/authorize"})
	b.redeem(b.minted.Code)

	if got := b.row(t).UsedFrom; got != "203.0.113.9" {
		t.Errorf("used_from = %q, want the TCP peer, not the forwarded header", got)
	}
}

func TestIdentityEchoRedeem_ShouldRefuseAMalformedBody(t *testing.T) {
	t.Parallel()

	b := newBootstrapEcho(t, &fakeAuthService{})
	req := httptest.NewRequest(http.MethodPost, "/identity/echo/redeem", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	b.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestIdentityEchoRedeem_ShouldRecordAnAuthorizationURLFailure(t *testing.T) {
	t.Parallel()

	b := newBootstrapEcho(t, &fakeAuthService{echoURLErr: errors.New("provider unavailable")})
	if w := b.redeem(b.minted.Code); w.Code == http.StatusOK {
		t.Fatalf("got status 200, want the error")
	}

	if got := b.row(t).Outcome; !strings.Contains(got, "provider unavailable") {
		t.Errorf("outcome = %q, want the failure", got)
	}
}

func TestBootstrapEcho_ShouldShowAndRecordTheExchangeError(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{
		echoURL: "https://idp.example.com/authorize", nonce: "n-1",
		echoCbErr: errors.New(`failed to exchange OIDC authorization code: oauth2: "invalid_client"`),
	}
	b := newBootstrapEcho(t, svc)

	w := b.roundTrip(t, "code=abc")

	assertSignInFailure(t, w, bootstrapEchoResultPath, "failed")
	tests := []struct {
		name string
		got  string
	}{
		{name: "should show the error on the page", got: decodeEchoFailure(t, w.Header().Get("Location")).Message},
		{name: "should record the error for the command", got: b.row(t).Outcome},
	}
	for _, tt := range tests {
		if !strings.Contains(tt.got, "invalid_client") {
			t.Errorf("%s: got %q, want the exchange error", tt.name, tt.got)
		}
	}
}

func TestBootstrapEcho_ShouldPassOnTheProviderRefusal(t *testing.T) {
	t.Parallel()

	b := newBootstrapEcho(t, &fakeAuthService{echoURL: "https://idp.example.com/authorize", nonce: "n-1"})

	w := b.roundTrip(t, "error=access_denied&error_description="+url.QueryEscape("user is not assigned to the application"))

	assertSignInFailure(t, w, bootstrapEchoResultPath, "refused")
	if got := decodeEchoFailure(t, w.Header().Get("Location")).Message; !strings.Contains(got, "not assigned") {
		t.Errorf("message = %q, want the provider's description", got)
	}
}

func TestBootstrapEcho_ShouldEstablishNoSessionAndWriteNoUsersRow(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{
		echoURL: "https://idp.example.com/authorize", nonce: "n-1",
		echoClaims: map[string]any{"sub": "sub-alice", "preferred_username": "alice"},
		mapping:    service.ClaimMapping{Subject: "sub", Username: "preferred_username"},
		identity:   &service.Identity{Subject: "sub-should-not-be-set", Username: "nobody"},
	}
	b := newBootstrapEcho(t, svc)
	w := b.roundTrip(t, "code=abc")

	var subject any = "unread"
	b.router.GET("/test/identity", func(c *gin.Context) {
		subject = sessions.Default(c).Get("identity_subject")
		c.String(http.StatusOK, "ok")
	})
	doRequest(b.router, httptest.NewRequest(http.MethodGet, "/test/identity", nil), w)

	var users int64
	if err := b.db.Model(&model.User{}).Count(&users).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if subject != nil || users != 0 {
		t.Errorf("got session subject %v and %d users rows, want neither", subject, users)
	}
}

func TestIdentityEcho_ShouldShowTheExchangeErrorOnTheAdminEchoPage(t *testing.T) {
	t.Parallel()

	svc := &fakeAuthService{
		echoURL: "https://idp.example.com/authorize", nonce: "n-1",
		echoCbErr: errors.New("failed to verify OIDC ID token: oidc: id token issued by a different provider"),
	}
	r := echoTestRouter(t, svc, &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}})

	start := httptest.NewRecorder()
	r.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/admin/identity/echo/start", nil))
	state := oidcStateFromSession(t, r, start)
	w := doRequest(r, httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(state), nil), start)

	if got := decodeEchoFailure(t, w.Header().Get("Location")).Message; !strings.Contains(got, "different provider") {
		t.Errorf("message = %q, want the verification error", got)
	}
}

func TestBootstrapEcho_ShouldSendAnExpiredStateBackToTheEchoPage(t *testing.T) {
	t.Parallel()

	b := newBootstrapEcho(t, &fakeAuthService{echoURL: "https://idp.example.com/authorize", nonce: "n-1"})
	redeemed := b.redeem(b.minted.Code)

	w := doRequest(b.router, httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state=not-the-state", nil), redeemed)

	assertSignInFailure(t, w, bootstrapEchoResultPath, "expired")
}
