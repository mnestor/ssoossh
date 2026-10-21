package service

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/oauth2"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/test/testdb"
)

// fakeAdmins answers AdminRecipients with a fixed list.
type fakeAdmins struct{ users []model.User }

// AdminRecipients returns the fixed list.
func (f *fakeAdmins) AdminRecipients(context.Context) ([]model.User, error) { return f.users, nil }

// userInfoLogin is an AuthService against a fake provider, wired with a
// health store over db, ready for one login.
type userInfoLogin struct {
	provider *fakeOIDCProvider
	svc      *AuthService
	health   *UserInfoHealthService
	db       *gorm.DB
}

// newUserInfoLogin builds the service. userInfo is what the provider's
// UserInfo endpoint answers (nil: discovery names none); enabled is
// authentication.userinfo.
func newUserInfoLogin(t *testing.T, userInfo map[string]any, enabled bool) *userInfoLogin {
	t.Helper()
	provider := newFakeOIDCProvider(t)
	provider.userInfo = userInfo
	cfg := newTestAuthConfig(provider, "client-1")
	cfg.AuthConfig.UserInfo = enabled
	db := testdb.New(t)
	svc, err := NewAuthService(context.Background(), cfg, db, provider.srv.Client())
	if err != nil {
		t.Fatalf("NewAuthService() error = %v", err)
	}
	health := NewUserInfoHealthService(db, svc.UserInfoEndpoint(), "https://ssh.example.com")
	svc.SetUserInfoHealth(health)
	return &userInfoLogin{provider: provider, svc: svc, health: health, db: db}
}

// signIn issues an ID token with claims and runs a login.
func (l *userInfoLogin) signIn(t *testing.T, claims map[string]any) (*Identity, error) {
	t.Helper()
	l.provider.nextID = l.provider.signIDToken(t, "sub-alice", "client-1", "nonce-1", claims)
	return l.svc.HandleCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier())
}

// echo issues an ID token with claims and runs a claims echo.
func (l *userInfoLogin) echo(t *testing.T, claims map[string]any) (*EchoTokens, error) {
	t.Helper()
	l.provider.nextID = l.provider.signIDToken(t, "sub-alice", "client-1", "nonce-1", claims)
	return l.svc.EchoCallback(context.Background(), "auth-code", "nonce-1", oauth2.GenerateVerifier())
}

func TestUserInfo_ShouldRefuseALoginWhoseUserInfoSubjectDiffers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, l *userInfoLogin) error
	}{
		{name: "should fail the login", run: func(t *testing.T, l *userInfoLogin) error {
			_, err := l.signIn(t, map[string]any{"preferred_username": "alice"})
			return err
		}},
		{name: "should fail the echo", run: func(t *testing.T, l *userInfoLogin) error {
			_, err := l.echo(t, map[string]any{"preferred_username": "alice"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := newUserInfoLogin(t, map[string]any{"sub": "sub-mallory", "groups": []string{"ssh-admins"}}, true)
			if err := tt.run(t, l); err == nil || !strings.Contains(err.Error(), "different subject") {
				t.Errorf("error = %v, want the subject mismatch", err)
			}
		})
	}
}

func TestUserInfo_ShouldLetTheIDTokenWinAndFillOnlyWhatItLacks(t *testing.T) {
	t.Parallel()

	l := newUserInfoLogin(t, map[string]any{
		"sub": "sub-alice", "email": "userinfo@example.com", "groups": []string{"ssh-admins"},
	}, true)

	identity, err := l.signIn(t, map[string]any{"preferred_username": "alice", "email": "idtoken@example.com"})
	if err != nil {
		t.Fatalf("HandleCallback() error = %v", err)
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should keep the ID token's email", ok: identity.Email == "idtoken@example.com"},
		{name: "should fill the groups the ID token lacked", ok: len(identity.Groups) == 1 && identity.Groups[0] == "ssh-admins"},
	}
	for _, tt := range tests {
		if !tt.ok {
			t.Errorf("%s: identity %+v", tt.name, identity)
		}
	}
}

func TestUserInfo_ShouldNameEachClaimsSourceOnTheEcho(t *testing.T) {
	t.Parallel()

	l := newUserInfoLogin(t, map[string]any{"sub": "sub-alice", "email": "u@example.com", "groups": []string{"a"}}, true)

	tokens, err := l.echo(t, map[string]any{"preferred_username": "alice", "email": "i@example.com"})
	if err != nil {
		t.Fatalf("EchoCallback() error = %v", err)
	}
	tests := []struct {
		claim string
		want  string
	}{
		{claim: "email", want: ClaimSourceIDToken},
		{claim: "groups", want: ClaimSourceUserInfo},
		{claim: "sub", want: ClaimSourceIDToken},
	}
	for _, tt := range tests {
		if got := tokens.Sources[tt.claim]; got != tt.want {
			t.Errorf("source of %s = %q, want %q", tt.claim, got, tt.want)
		}
	}
	if tokens.UserInfo != UserInfoUsed {
		t.Errorf("userinfo status = %q, want used", tokens.UserInfo)
	}
}

func TestUserInfo_ShouldSkipTheCallWhenItShouldNotRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		userInfo   map[string]any
		enabled    bool
		wantStatus UserInfoStatus
	}{
		{name: "should skip silently when discovery names no endpoint", enabled: true, wantStatus: UserInfoNoEndpoint},
		{name: "should never call when the config turns it off", userInfo: map[string]any{"sub": "sub-alice"}, wantStatus: UserInfoOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := newUserInfoLogin(t, tt.userInfo, tt.enabled)
			tokens, err := l.echo(t, map[string]any{"preferred_username": "alice"})
			if err != nil {
				t.Fatalf("EchoCallback() error = %v", err)
			}
			if tokens.UserInfo != tt.wantStatus || l.provider.userInfoCalls.Load() != 0 {
				t.Errorf("status = %q with %d calls, want %q and none", tokens.UserInfo, l.provider.userInfoCalls.Load(), tt.wantStatus)
			}
		})
	}
}

func TestUserInfo_ShouldContinueWithTheIDTokenWhenTheCallFails(t *testing.T) {
	t.Parallel()

	l := newUserInfoLogin(t, map[string]any{"sub": "sub-alice"}, true)
	l.provider.userInfoStatus = http.StatusBadGateway

	identity, err := l.signIn(t, map[string]any{"preferred_username": "alice"})
	if err != nil {
		t.Fatalf("HandleCallback() error = %v, want the login to go on", err)
	}
	state, _ := l.health.State(context.Background())
	if identity.Username != "alice" || state.ConsecutiveFailures != 1 || !strings.Contains(state.LastError, "502") {
		t.Errorf("identity %+v, state %+v, want the login and one counted failure", identity, state)
	}
}

func TestUserInfo_ShouldStillRequireTheUsernameWhenTheCallFails(t *testing.T) {
	t.Parallel()

	l := newUserInfoLogin(t, map[string]any{"sub": "sub-alice", "preferred_username": "alice"}, true)
	l.provider.userInfoStatus = http.StatusInternalServerError

	if _, err := l.signIn(t, map[string]any{}); err == nil || !strings.Contains(err.Error(), "username") {
		t.Errorf("error = %v, want the missing username", err)
	}
}

func TestScrubUserInfoError_ShouldCutTheAccessTokenAndCap(t *testing.T) {
	t.Parallel()

	got := scrubUserInfoError("401 for tok-123: "+strings.Repeat("x", 2*userInfoErrorLength), "tok-123")
	if strings.Contains(got, "tok-123") || len(got) != userInfoErrorLength {
		t.Errorf("got %d bytes containing the token: %t", len(got), strings.Contains(got, "tok-123"))
	}
}

func TestScrubUserInfoError_ShouldKeepValidUTF8WhenTheCapSplitsACharacter(t *testing.T) {
	t.Parallel()

	got := scrubUserInfoError("x"+strings.Repeat("\u00e9", userInfoErrorLength), "")
	if !utf8.ValidString(got) || len(got) != userInfoErrorLength-1 {
		t.Errorf("got %d bytes, valid UTF-8 %t, want %d valid bytes", len(got), utf8.ValidString(got), userInfoErrorLength-1)
	}
}

// healthHarness is a UserInfoHealthService with a settable clock, a
// capturing notifier, one admin and an auditor over db.
type healthHarness struct {
	*UserInfoHealthService
	notifier *capturingNotifier
	clock    *time.Time
}

// newHealth builds one instance over db; several over the same db stand
// in for several servers.
func newHealth(db *gorm.DB, clock *time.Time) *healthHarness {
	h := NewUserInfoHealthService(db, "https://idp.example.com/userinfo", "https://ssh.example.com")
	h.now = func() time.Time { return *clock }
	h.SetAuditor(NewAuditService(&config.Config{}, db))
	n := &capturingNotifier{}
	h.SetNotifications(n, &fakeAdmins{users: []model.User{{ID: "admin-1"}}}, true)
	return &healthHarness{UserInfoHealthService: h, notifier: n, clock: clock}
}

// kinds counts the captured mail of kind.
func (h *healthHarness) kinds(kind notify.Kind) int {
	n := 0
	for _, e := range h.notifier.captured() {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func TestUserInfoHealth_ShouldPauseAtTheThresholdAndNotBefore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		failures   int
		wantPaused bool
	}{
		{name: "should not pause at 9", failures: 9},
		{name: "should pause at 10", failures: 10, wantPaused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
			h := newHealth(testdb.New(t), &clock)
			for range tt.failures {
				h.RecordFailure(context.Background(), "502 Bad Gateway", false)
			}
			state, _ := h.State(context.Background())
			if state.Paused != tt.wantPaused {
				t.Errorf("paused = %t after %d failures, want %t", state.Paused, tt.failures, tt.wantPaused)
			}
		})
	}
}

func TestUserInfoHealth_ShouldCountFailuresAcrossInstances(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	db := testdb.New(t)
	first, second := newHealth(db, &clock), newHealth(db, &clock)
	for i := range 10 {
		if i%2 == 0 {
			first.RecordFailure(context.Background(), "timeout", false)
		} else {
			second.RecordFailure(context.Background(), "timeout", false)
		}
	}

	state, _ := first.State(context.Background())
	pauses := first.kinds(notify.KindUserInfoPaused) + second.kinds(notify.KindUserInfoPaused)
	if !state.Paused || state.ConsecutiveFailures != 10 || pauses != 1 {
		t.Errorf("state %+v with %d pause mails, want paused at 10 with one mail between the instances", state, pauses)
	}
}

func TestUserInfoHealth_ShouldResetTheCountOnASuccessBeforeTheThreshold(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	h := newHealth(testdb.New(t), &clock)
	for range 9 {
		h.RecordFailure(context.Background(), "timeout", false)
	}
	h.RecordSuccess(context.Background())
	h.RecordFailure(context.Background(), "timeout", false)

	state, _ := h.State(context.Background())
	if state.Paused || state.ConsecutiveFailures != 1 || state.LastSuccessAt == nil {
		t.Errorf("state %+v, want one failure since the success and no pause", state)
	}
}

// pausedHealth returns an instance whose calls are paused, at clock.
func pausedHealth(t *testing.T, db *gorm.DB, clock *time.Time) *healthHarness {
	t.Helper()
	h := newHealth(db, clock)
	for range DefaultUserInfoPolicy().PauseAfter {
		h.RecordFailure(context.Background(), "timeout", false)
	}
	return h
}

func TestUserInfoHealth_ShouldLetOneProbeThroughPerWindowAcrossInstances(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	db := testdb.New(t)
	first := pausedHealth(t, db, &clock)
	second := newHealth(db, &clock)

	call, _ := first.Gate(context.Background())
	if call {
		t.Fatal("a login inside the first window called UserInfo")
	}

	clock = clock.Add(DefaultUserInfoPolicy().ProbeEvery + time.Second)
	var mu sync.Mutex
	probes := 0
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			instance := first
			if i%2 == 1 {
				instance = second
			}
			if call, probe := instance.Gate(context.Background()); call && probe {
				mu.Lock()
				probes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if probes != 1 {
		t.Errorf("%d probes ran in one window, want exactly one", probes)
	}
}

func TestUserInfoHealth_ShouldResumeAndMailOnceWhenAProbeSucceeds(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	db := testdb.New(t)
	h := pausedHealth(t, db, &clock)
	clock = clock.Add(DefaultUserInfoPolicy().ProbeEvery)
	if call, probe := h.Gate(context.Background()); !call || !probe {
		t.Fatal("the window's first login was not the probe")
	}
	h.RecordSuccess(context.Background())
	h.RecordSuccess(context.Background())

	state, _ := h.State(context.Background())
	call, probe := h.Gate(context.Background())
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should resume the calls", ok: !state.Paused && state.ConsecutiveFailures == 0 && state.NextProbeAt == nil},
		{name: "should call on every login again", ok: call && !probe},
		{name: "should mail the pause once", ok: h.kinds(notify.KindUserInfoPaused) == 1},
		{name: "should mail the resume once", ok: h.kinds(notify.KindUserInfoResumed) == 1},
		{name: "should audit the pause", ok: len(auditPayloads(t, db, AuditAuthUserInfoPaused)) == 1},
		{name: "should audit the resume", ok: len(auditPayloads(t, db, AuditAuthUserInfoResumed)) == 1},
	}
	for _, tt := range tests {
		if !tt.ok {
			t.Errorf("%s: state %+v", tt.name, state)
		}
	}
}

func TestUserInfoHealth_ShouldStayPausedWithoutASecondMailWhenAProbeFails(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	h := pausedHealth(t, testdb.New(t), &clock)
	clock = clock.Add(DefaultUserInfoPolicy().ProbeEvery)
	_, probe := h.Gate(context.Background())
	h.RecordFailure(context.Background(), "still down", probe)

	state, _ := h.State(context.Background())
	wantNext := clock.Add(DefaultUserInfoPolicy().ProbeEvery)
	if !state.Paused || state.NextProbeAt == nil || !state.NextProbeAt.Equal(wantNext) || h.kinds(notify.KindUserInfoPaused) != 1 {
		t.Errorf("state %+v with %d pause mails, want still paused, the window restarted at the failure, one mail", state, h.kinds(notify.KindUserInfoPaused))
	}
}

func TestUserInfo_ShouldNotCallWhilePausedExceptForTheProbe(t *testing.T) {
	t.Parallel()

	l := newUserInfoLogin(t, map[string]any{"sub": "sub-alice", "groups": []string{"a"}}, true)
	clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	l.health.now = func() time.Time { return clock }
	for range DefaultUserInfoPolicy().PauseAfter {
		l.health.RecordFailure(context.Background(), "timeout", false)
	}

	paused, err := l.echo(t, map[string]any{"preferred_username": "alice"})
	if err != nil {
		t.Fatalf("EchoCallback() error = %v", err)
	}
	callsWhilePaused := l.provider.userInfoCalls.Load()
	clock = clock.Add(DefaultUserInfoPolicy().ProbeEvery)
	probed, err := l.echo(t, map[string]any{"preferred_username": "alice"})
	if err != nil {
		t.Fatalf("EchoCallback() error = %v", err)
	}

	if paused.UserInfo != UserInfoPaused || callsWhilePaused != 0 || probed.UserInfo != UserInfoUsed || l.provider.userInfoCalls.Load() != 1 {
		t.Errorf("paused status %q after %d calls, probe status %q after %d calls", paused.UserInfo, callsWhilePaused, probed.UserInfo, l.provider.userInfoCalls.Load())
	}
}

func TestUserInfoHealth_ShouldNotMailWhenMailIsOff(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	h := newHealth(testdb.New(t), &clock)
	h.mailOn = false
	for range DefaultUserInfoPolicy().PauseAfter {
		h.RecordFailure(context.Background(), "timeout", false)
	}

	if h.kinds(notify.KindUserInfoPaused) != 0 {
		t.Error("mailed the pause with mail off")
	}
}

// fakeUserInfoDiagnostics is a canned UserInfoDiagnostics.
type fakeUserInfoDiagnostics struct {
	enabled  bool
	endpoint string
	state    model.UserInfoHealth
}

func (f *fakeUserInfoDiagnostics) UserInfoEnabled() bool    { return f.enabled }
func (f *fakeUserInfoDiagnostics) UserInfoEndpoint() string { return f.endpoint }
func (f *fakeUserInfoDiagnostics) UserInfoState(context.Context) (model.UserInfoHealth, error) {
	return f.state, nil
}

func TestCheckUserInfo_ShouldReportTheEndpointsState(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	next := at.Add(15 * time.Minute)
	endpoint := "https://idp.example.com/userinfo"
	tests := []struct {
		name        string
		diag        *fakeUserInfoDiagnostics
		wantStatus  DiagnosticStatus
		wantSummary string
		wantFinding string
	}{
		{name: "should say the config turns it off", diag: &fakeUserInfoDiagnostics{}, wantStatus: DiagnosticSkipped, wantFinding: "authentication.userinfo: off"},
		{name: "should say discovery names no endpoint", diag: &fakeUserInfoDiagnostics{enabled: true}, wantStatus: DiagnosticSkipped, wantFinding: "not in discovery"},
		{name: "should call a clean endpoint healthy", diag: &fakeUserInfoDiagnostics{enabled: true, endpoint: endpoint, state: model.UserInfoHealth{LastSuccessAt: &at}}, wantStatus: DiagnosticOK, wantSummary: "Healthy", wantFinding: "Last success: 2026-10-21T09:00:00Z"},
		{name: "should count failures in a row", diag: &fakeUserInfoDiagnostics{enabled: true, endpoint: endpoint, state: model.UserInfoHealth{ConsecutiveFailures: 3, LastError: "502 Bad Gateway", LastFailureAt: &at}}, wantStatus: DiagnosticWarn, wantSummary: "3 call(s) in a row", wantFinding: "Last error: 502 Bad Gateway"},
		{name: "should say since when it is paused and the next probe", diag: &fakeUserInfoDiagnostics{enabled: true, endpoint: endpoint, state: model.UserInfoHealth{Paused: true, PausedAt: &at, NextProbeAt: &next, ConsecutiveFailures: 10}}, wantStatus: DiagnosticCritical, wantSummary: "Paused since 2026-10-21T09:00:00Z", wantFinding: "Next probe: 2026-10-21T09:15:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := NewDiagnosticsService(&config.Config{}, AppSecurityHeaders{}, nil)
			s.SetUserInfo(tt.diag)
			got := s.checkUserInfo(context.Background())
			if got.Status != tt.wantStatus || !strings.Contains(got.Summary, tt.wantSummary) || !strings.Contains(strings.Join(got.Findings, "\n"), tt.wantFinding) {
				t.Errorf("got %+v, want status %q, summary with %q, a finding with %q", got, tt.wantStatus, tt.wantSummary, tt.wantFinding)
			}
		})
	}
}
