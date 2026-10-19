package upstream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/blackhole"
	"github.com/mnestor/ssoossh/nssd/config"
)

// fakeClock is a settable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the current fake time.
func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the clock on.
func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// fakeAPI is an api.NSSClient answering from a function, recording calls,
// idle-connection closes and, for user lookups by name, each attempt's
// deadline.
type fakeAPI struct {
	url      string
	key      string
	answer   func(url string) (*apitypes.NSSUserResponse, error)
	calls    *atomic.Int32
	closes   *atomic.Int32
	deadline func(time.Duration)
}

// GetNSSUserByName implements api.NSSClient.
func (f *fakeAPI) GetNSSUserByName(ctx context.Context, _ string) (*apitypes.NSSUserResponse, error) {
	f.calls.Add(1)
	if d, ok := ctx.Deadline(); ok && f.deadline != nil {
		f.deadline(time.Until(d))
	}
	return f.answer(f.url)
}

// CloseIdleConnections implements api.IdleCloser.
func (f *fakeAPI) CloseIdleConnections() { f.closes.Add(1) }

// GetNSSUserByUID implements api.NSSClient.
func (f *fakeAPI) GetNSSUserByUID(context.Context, uint32) (*apitypes.NSSUserResponse, error) {
	f.calls.Add(1)
	return f.answer(f.url)
}

// GetNSSGroupByName implements api.NSSClient.
func (f *fakeAPI) GetNSSGroupByName(context.Context, string) (*apitypes.NSSGroupResponse, error) {
	f.calls.Add(1)
	_, err := f.answer(f.url)
	return &apitypes.NSSGroupResponse{Name: "devs", GID: 5100}, err
}

// GetNSSGroupByGID implements api.NSSClient.
func (f *fakeAPI) GetNSSGroupByGID(context.Context, uint32) (*apitypes.NSSGroupResponse, error) {
	f.calls.Add(1)
	_, err := f.answer(f.url)
	return &apitypes.NSSGroupResponse{Name: "devs", GID: 5100}, err
}

// harness is a Client over fakes.
type harness struct {
	client  *Client
	clock   *fakeClock
	calls   atomic.Int32
	closes  atomic.Int32
	keyPath string
	// deadlines is each name lookup attempt's time to its deadline.
	deadlines   []time.Duration
	deadlinesMu sync.Mutex
	built       []api.Config
	builtMu     sync.Mutex
	logs        *bytes.Buffer
	answer      func(url string) (*apitypes.NSSUserResponse, error)
	answerMu    sync.Mutex
}

// setAnswer changes what the fake server answers.
func (h *harness) setAnswer(fn func(url string) (*apitypes.NSSUserResponse, error)) {
	h.answerMu.Lock()
	defer h.answerMu.Unlock()
	h.answer = fn
}

// writeKey writes the fleet key file.
func writeKey(t *testing.T, path, id, secret string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(id+"\n"+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newHarness builds a Client; mutate adjusts its options.
func newHarness(t *testing.T, mutate func(*config.Config, *Options)) *harness {
	t.Helper()
	h := &harness{clock: &fakeClock{now: time.Unix(1_800_000_000, 0)}, logs: &bytes.Buffer{}}
	h.keyPath = filepath.Join(t.TempDir(), "nss.key")
	writeKey(t, h.keyPath, "nss_k1", "secret1")
	h.answer = func(string) (*apitypes.NSSUserResponse, error) {
		return &apitypes.NSSUserResponse{Name: "alice", UID: 5001}, nil
	}

	cfg := config.Defaults()
	cfg.Server = "https://sso.example.com"
	cfg.TLSSPKIPin = []string{"AAAA"}
	cfg.APIKeyFile = h.keyPath
	opts := Options{
		Config: &cfg,
		Log:    slog.New(slog.NewTextHandler(h.logs, nil)),
		Now:    h.clock.Now,
		NewClient: func(c api.Config) (api.NSSClient, error) {
			h.builtMu.Lock()
			h.built = append(h.built, c)
			h.builtMu.Unlock()
			return &fakeAPI{url: c.ServerURL, key: c.KeyID, calls: &h.calls, closes: &h.closes, answer: func(url string) (*apitypes.NSSUserResponse, error) {
				h.answerMu.Lock()
				fn := h.answer
				h.answerMu.Unlock()
				return fn(url)
			}, deadline: func(d time.Duration) {
				h.deadlinesMu.Lock()
				h.deadlines = append(h.deadlines, d)
				h.deadlinesMu.Unlock()
			}}, nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	if mutate != nil {
		mutate(&cfg, &opts)
	}
	client, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	h.client = client
	return h
}

// errTransport is a failure with no HTTP answer.
var errTransport = errors.New("dial tcp: connection refused")

func TestNewShouldRefuseAnUnreadableKeyWhenStarting(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.APIKeyFile = filepath.Join(t.TempDir(), "absent")
	if _, err := New(Options{Config: &cfg, Log: slog.Default()}); err == nil || !strings.Contains(err.Error(), "fleet key") {
		t.Fatalf("got %v", err)
	}
}

func TestNewShouldUseTheRealDefaultsWhenNoFakesAreGiven(t *testing.T) {
	t.Parallel()
	keyPath := filepath.Join(t.TempDir(), "nss.key")
	writeKey(t, keyPath, "nss_k1", "s")
	cfg := config.Defaults()
	cfg.Server = "https://127.0.0.1:1"
	cfg.TLSSPKIPin = []string{"47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="}
	cfg.APIKeyFile = keyPath
	cfg.Timeout = 2 * time.Second
	cfg.ReconnectionRetries = 1
	c, err := New(Options{Config: &cfg, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	// Port 1 refuses at once: the real client, the real sleep and the
	// retry all run, and the outcome is offline.
	if _, err := c.UserByName(t.Context(), "alice"); !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v, want ErrOffline", err)
	}
}

func TestLookupClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		answer    error
		want      error
		wantState string
	}{
		{name: "should answer when the server finds the record", want: nil, wantState: StateOnline},
		{name: "should report not found when the server says nss_not_found", answer: &api.NSSError{StatusCode: 404, ErrorCode: apitypes.ErrorCodeNSSNotFound}, want: ErrNotFound, wantState: StateOnline},
		{name: "should report invalid when the server says nss_bad_request", answer: &api.NSSError{StatusCode: 400, ErrorCode: apitypes.ErrorCodeNSSBadRequest}, want: ErrInvalid, wantState: StateOnline},
		{name: "should latch when the server answers 401", answer: &api.NSSError{StatusCode: 401}, want: ErrUnauthorized, wantState: StateUnauthorized},
		{name: "should wait when the server answers 429", answer: &api.NSSError{StatusCode: 429, RetryAfter: 30 * time.Second}, want: ErrRateLimited, wantState: StateRateLimited},
		{name: "should go offline when the server says nss_disabled", answer: &api.NSSError{StatusCode: 404, ErrorCode: apitypes.ErrorCodeNSSDisabled}, want: ErrOffline, wantState: StateOffline},
		{name: "should go offline when the server fails", answer: &api.NSSError{StatusCode: 503}, want: ErrOffline, wantState: StateOffline},
		{name: "should go offline when the transport fails", answer: errTransport, want: ErrOffline, wantState: StateOffline},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, nil)
			if tt.answer != nil {
				h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return nil, tt.answer })
			}
			_, err := h.client.UserByName(t.Context(), "alice")
			if !errors.Is(err, tt.want) || (tt.want == nil) != (err == nil) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if got := h.client.Status().State; got != tt.wantState {
				t.Fatalf("got state %q, want %q", got, tt.wantState)
			}
		})
	}
}

func TestEveryLookupKindShouldGoThroughTheSamePathWhenCalled(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	ctx := t.Context()
	if u, err := h.client.UserByUID(ctx, 5001); err != nil || u.Name != "alice" {
		t.Fatalf("by uid: %+v, %v", u, err)
	}
	if g, err := h.client.GroupByName(ctx, "devs"); err != nil || g.GID != 5100 {
		t.Fatalf("group by name: %+v, %v", g, err)
	}
	if g, err := h.client.GroupByGID(ctx, 5100); err != nil || g.Name != "devs" {
		t.Fatalf("group by gid: %+v, %v", g, err)
	}
	if n := h.calls.Load(); n != 3 {
		t.Fatalf("got %d calls", n)
	}
	b := h.built[0]
	if b.KeyID != "nss_k1" || b.KeySecret != "secret1" || b.ServerURL != "https://sso.example.com" || b.TLSSPKIPins[0] != "AAAA" || b.RequestTimeout != 5*time.Second || b.DialTimeout != 2*time.Second || !strings.HasPrefix(b.UserAgent, "ssoossh-nss/") {
		t.Fatalf("got api config %+v", b)
	}
}

func TestClientForShouldCarryTheTrustChoiceWhenBuildingTheAPIClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		caFile      string
		pins        []string
		wantCAFile  string
		wantSystem  bool
		wantStatus  Status
		wantWarning bool
	}{
		{name: "should set TLSSystemRoots when tls_ca_file is system", caFile: "system", wantSystem: true,
			wantStatus: Status{TLSCAFile: "system"}, wantWarning: true},
		{name: "should set both when tls_ca_file is system and pins are set", caFile: "system", pins: []string{"AAAA", "BBBB"}, wantSystem: true,
			wantStatus: Status{TLSCAFile: "system", TLSPins: 2}},
		{name: "should pass the path as TLSCAFile when tls_ca_file is a path", caFile: "/etc/ssoossh/ca.pem", wantCAFile: "/etc/ssoossh/ca.pem",
			wantStatus: Status{TLSCAFile: "/etc/ssoossh/ca.pem"}},
		{name: "should set neither when only a pin is set", pins: []string{"AAAA"},
			wantStatus: Status{TLSPins: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(c *config.Config, _ *Options) { c.TLSCAFile, c.TLSSPKIPin = tt.caFile, tt.pins })
			if _, err := h.client.UserByName(t.Context(), "alice"); err != nil {
				t.Fatal(err)
			}
			b := h.built[0]
			if b.TLSCAFile != tt.wantCAFile || b.TLSSystemRoots != tt.wantSystem || len(b.TLSSPKIPins) != len(tt.pins) {
				t.Fatalf("got TLSCAFile %q, TLSSystemRoots %v, %d pins", b.TLSCAFile, b.TLSSystemRoots, len(b.TLSSPKIPins))
			}
			st := h.client.Status()
			if st.TLSCAFile != tt.wantStatus.TLSCAFile || st.TLSPins != tt.wantStatus.TLSPins {
				t.Fatalf("got status trust %q, %d pins", st.TLSCAFile, st.TLSPins)
			}
			warned := strings.Contains(h.logs.String(), "level=WARN msg=\"tls_ca_file is system: every CA in this host's trust store can answer for the server")
			if warned != tt.wantWarning {
				t.Fatalf("warned = %v, want %v; logs:\n%s", warned, tt.wantWarning, h.logs.String())
			}
		})
	}
}

func TestUnauthorizedLatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return nil, &api.NSSError{StatusCode: 401} })
	ctx := t.Context()

	if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return &apitypes.NSSUserResponse{Name: "alice"}, nil })

	// Latched: no request goes out, however long it has been.
	for range 3 {
		h.clock.Advance(10 * time.Minute)
		if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("should stay latched until SIGHUP or a key change, got %v", err)
		}
	}
	if n := h.calls.Load(); n != 1 {
		t.Fatalf("should stop fetching when latched, made %d calls", n)
	}
	if n := strings.Count(h.logs.String(), "refused the fleet key"); n != 4 {
		t.Fatalf("should log at error once per minute while latched, logged %d times", n)
	}
	h.clock.Advance(time.Second)
	if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("still latched")
	}
	if n := strings.Count(h.logs.String(), "refused the fleet key"); n != 4 {
		t.Fatalf("should not log again within the minute, logged %d times", n)
	}

	h.client.Reset()
	if _, err := h.client.UserByName(ctx, "alice"); err != nil {
		t.Fatalf("should clear the latch when reset by SIGHUP, got %v", err)
	}
}

func TestUnauthorizedLatchShouldClearWhenTheKeyFileChanges(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return nil, &api.NSSError{StatusCode: 401} })
	ctx := t.Context()
	if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}

	// A key file that cannot be read keeps the latch and the old key.
	writeKey(t, h.keyPath, "nss_k1", "secret-insecure")
	if err := os.Chmod(h.keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(h.logs.String(), "cannot read the fleet key file") {
		t.Fatalf("should log an unreadable key file, got %s", h.logs)
	}

	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return &apitypes.NSSUserResponse{Name: "alice"}, nil })
	writeKey(t, h.keyPath, "nss_k1", "secret2")
	if _, err := h.client.UserByName(ctx, "alice"); err != nil {
		t.Fatalf("should clear the latch when the key file changes, got %v", err)
	}
	if last := h.built[len(h.built)-1]; last.KeySecret != "secret2" {
		t.Fatalf("should use the new secret, built %+v", last)
	}
	if got := h.client.Status().KeyID; got != "nss_k1" {
		t.Fatalf("got key id %q", got)
	}
}

func TestRateLimitedShouldHonourRetryAfter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		retryAfter time.Duration
		wait       time.Duration
	}{
		{name: "should wait the server's Retry-After when it sends one", retryAfter: 30 * time.Second, wait: 30 * time.Second},
		{name: "should wait offline_timeout when the server sends none", retryAfter: 0, wait: 60 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, nil)
			h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) {
				return nil, &api.NSSError{StatusCode: 429, RetryAfter: tt.retryAfter}
			})
			ctx := t.Context()
			if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrRateLimited) {
				t.Fatalf("got %v", err)
			}
			h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return &apitypes.NSSUserResponse{Name: "alice"}, nil })
			h.clock.Advance(tt.wait - time.Second)
			if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrRateLimited) {
				t.Fatalf("should not send before Retry-After, got %v", err)
			}
			if st := h.client.Status(); st.RetryAt.IsZero() || st.State != StateRateLimited {
				t.Fatalf("got status %+v", st)
			}
			h.clock.Advance(time.Second)
			if _, err := h.client.UserByName(ctx, "alice"); err != nil {
				t.Fatalf("should send again once Retry-After has passed, got %v", err)
			}
			if n := h.calls.Load(); n != 2 {
				t.Fatalf("got %d calls", n)
			}
			if !strings.Contains(h.logs.String(), "reachable again") {
				t.Fatalf("should log the recovery, got %s", h.logs)
			}
		})
	}
}

func TestOfflineBackoffShouldDoubleWhenProbesKeepFailing(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(c *config.Config, _ *Options) { c.ReconnectionRetries = 0; c.OfflineTimeout = 10 * time.Second })
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return nil, errTransport })
	ctx := t.Context()

	waits := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second}
	if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v", err)
	}
	for i, wait := range waits {
		h.clock.Advance(wait - time.Millisecond)
		if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrOffline) {
			t.Fatalf("probe %d: got %v", i, err)
		}
		if n := h.calls.Load(); n != int32(i+1) {
			t.Fatalf("should not probe before the backoff ends, made %d calls before probe %d", n, i)
		}
		h.clock.Advance(time.Millisecond)
		if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrOffline) {
			t.Fatalf("probe %d: got %v", i, err)
		}
	}
	st := h.client.Status()
	if st.State != StateOffline || !strings.Contains(st.LastError, "connection refused") {
		t.Fatalf("got status %+v", st)
	}
	if n := strings.Count(h.logs.String(), "unreachable"); n != 1 {
		t.Fatalf("should log going offline once, logged %d times", n)
	}
}

func TestOfflineBackoffShouldStopDoublingWhenItReachesTheCap(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(c *config.Config, _ *Options) { c.ReconnectionRetries = 0; c.OfflineTimeout = 40 * time.Minute })
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return nil, errTransport })
	ctx := t.Context()
	for range 4 {
		_, _ = h.client.UserByName(ctx, "alice")
		h.clock.Advance(maxOfflineBackoff)
	}
	st := h.client.Status()
	if got := st.RetryAt.Sub(h.clock.Now().Add(-maxOfflineBackoff)); got != maxOfflineBackoff {
		t.Fatalf("got backoff %v, want the %v cap", got, maxOfflineBackoff)
	}
}

func TestEveryAttemptShouldGoToTheConfiguredServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		answer    error
		want      error
		wantState string
		wantCalls int
	}{
		{name: "should ask the configured server when it answers", wantState: StateOnline, wantCalls: 1},
		{name: "should retry the configured server when the transport fails", answer: errTransport, want: ErrOffline, wantState: StateOffline, wantCalls: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, nil)
			var tried []string
			var mu sync.Mutex
			h.setAnswer(func(url string) (*apitypes.NSSUserResponse, error) {
				mu.Lock()
				tried = append(tried, url)
				mu.Unlock()
				if tt.answer != nil {
					return nil, tt.answer
				}
				return &apitypes.NSSUserResponse{Name: "alice"}, nil
			})
			if _, err := h.client.UserByName(t.Context(), "alice"); !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if len(tried) != tt.wantCalls {
				t.Fatalf("got %d attempts, want %d", len(tried), tt.wantCalls)
			}
			for _, url := range tried {
				if url != "https://sso.example.com" {
					t.Fatalf("tried %v, want only the configured server", tried)
				}
			}
			if st := h.client.Status(); st.Server != "https://sso.example.com" || st.State != tt.wantState {
				t.Fatalf("got status %+v, want the configured server %s", st, tt.wantState)
			}
		})
	}
}

func TestResetShouldBuildAFreshClientWhenTheNextLookupRuns(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	ctx := t.Context()
	if _, err := h.client.UserByName(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.client.UserByName(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	h.client.Reset()
	if _, err := h.client.UserByName(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	h.builtMu.Lock()
	defer h.builtMu.Unlock()
	if len(h.built) != 2 {
		t.Fatalf("built %d clients, want one before the reset and one after", len(h.built))
	}
}

func TestRetriesShouldPauseBetweenAttemptsWhenTheTransportFails(t *testing.T) {
	t.Parallel()
	var pauses atomic.Int32
	h := newHarness(t, func(c *config.Config, o *Options) {
		c.ReconnectionRetries = 3
		o.Sleep = func(_ context.Context, d time.Duration) error {
			if d != retryPause {
				t.Errorf("paused %v", d)
			}
			pauses.Add(1)
			return nil
		}
	})
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return nil, errTransport })
	if _, err := h.client.UserByName(t.Context(), "alice"); !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v", err)
	}
	if h.calls.Load() != 4 || pauses.Load() != 3 {
		t.Fatalf("got %d calls and %d pauses, want 4 and 3", h.calls.Load(), pauses.Load())
	}
}

func TestRetriesShouldStopWhenThePauseIsInterrupted(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(c *config.Config, o *Options) {
		c.ReconnectionRetries = 3
		o.Sleep = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	})
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return nil, errTransport })
	if _, err := h.client.UserByName(t.Context(), "alice"); !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v", err)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("got %d calls", h.calls.Load())
	}
}

func TestClientBuildFailureShouldGoOfflineWhenTheConfigIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(_ *config.Config, o *Options) {
		o.NewClient = func(api.Config) (api.NSSClient, error) { return nil, errors.New("bad pin") }
	})
	if _, err := h.client.UserByName(t.Context(), "alice"); !errors.Is(err, ErrOffline) || !strings.Contains(err.Error(), "bad pin") {
		t.Fatalf("got %v", err)
	}
}

func TestCancelledCallerShouldChangeNoStateWhenTheDaemonStops(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) {
		cancel()
		return nil, context.Canceled
	})
	if _, err := h.client.UserByName(ctx, "alice"); !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if st := h.client.Status().State; st != StateUnknown {
		t.Fatalf("got state %q", st)
	}
}

func TestConcurrencyCapShouldRefuseAtOnceWhenEightLookupsAreInFlight(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	release := make(chan struct{})
	started := make(chan struct{}, MaxInFlight)
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) {
		started <- struct{}{}
		<-release
		return &apitypes.NSSUserResponse{Name: "alice"}, nil
	})
	ctx := t.Context()
	var wg sync.WaitGroup
	for range MaxInFlight {
		wg.Go(func() {
			if _, err := h.client.UserByName(ctx, "alice"); err != nil {
				t.Errorf("an in-cap lookup failed: %v", err)
			}
		})
	}
	for range MaxInFlight {
		<-started
	}
	start := time.Now()
	if _, err := h.client.UserByName(ctx, "bob"); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("should refuse at once, not wait for a slot")
	}
	close(release)
	wg.Wait()
	if _, err := h.client.UserByName(ctx, "bob"); err != nil {
		t.Fatalf("should take lookups again once slots free, got %v", err)
	}
}

func TestSleepContext(t *testing.T) {
	t.Parallel()
	if err := sleepContext(t.Context(), time.Millisecond); err != nil {
		t.Fatalf("should return nil when the wait ends, got %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("should return the context's error when it ends first, got %v", err)
	}
}

func TestAttemptTimeout(t *testing.T) {
	t.Parallel()

	const ms = time.Millisecond
	tests := []struct {
		name           string
		left           time.Duration
		answerDeadline time.Duration
		last           bool
		want           time.Duration
	}{
		{name: "should leave a second for the retry inside the answer deadline when the defaults apply", left: 5 * time.Second, answerDeadline: 2500 * ms, want: 1500 * ms},
		{name: "should give the last attempt everything left when it is the last", left: 5 * time.Second, answerDeadline: 2500 * ms, last: true, want: 5 * time.Second},
		{name: "should take half of what is left when the timeout is shorter than the answer deadline", left: time.Second, answerDeadline: 2500 * ms, want: 500 * ms},
		{name: "should halve the answer deadline when it is too short to spare the reserve", left: 5 * time.Second, answerDeadline: time.Second, want: 500 * ms},
		{name: "should halve the answer deadline when it is barely longer than the reserve", left: 5 * time.Second, answerDeadline: 1200 * ms, want: 600 * ms},
		{name: "should cap a long answer deadline by half of what is left", left: 5 * time.Second, answerDeadline: 20 * time.Second, want: 2500 * ms},
		{name: "should allow up to the answer deadline less the reserve when the timeout is long", left: 30 * time.Second, answerDeadline: 10 * time.Second, want: 9 * time.Second},
		{name: "should split what is left when there is no answer deadline", left: 4 * time.Second, answerDeadline: 0, want: 2 * time.Second},
		{name: "should give nothing when nothing is left", left: 0, answerDeadline: 2500 * ms, want: 0},
		{name: "should pass a past deadline through when it is already gone", left: -ms, answerDeadline: 2500 * ms, want: -ms},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := attemptTimeout(tt.left, tt.answerDeadline, tt.last); got != tt.want {
				t.Errorf("attemptTimeout(%s, %s, %v) = %s, want %s", tt.left, tt.answerDeadline, tt.last, got, tt.want)
			}
		})
	}
}

func TestEachAttemptShouldGetItsOwnDeadlineWhenTheLookupIsRetried(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) { return nil, errTransport })
	if _, err := h.client.UserByName(t.Context(), "alice"); !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v", err)
	}
	h.deadlinesMu.Lock()
	defer h.deadlinesMu.Unlock()
	// Defaults: 5 s timeout, 2.5 s answer deadline, 3 retries. The fake
	// fails at once, so each attempt but the last sees about 1.5 s and the
	// last about the whole 5 s.
	want := []time.Duration{1500 * time.Millisecond, 1500 * time.Millisecond, 1500 * time.Millisecond, 5 * time.Second}
	if len(h.deadlines) != len(want) {
		t.Fatalf("got %d attempts, want %d", len(h.deadlines), len(want))
	}
	for i, d := range h.deadlines {
		if d > want[i] || d < want[i]-100*time.Millisecond {
			t.Errorf("attempt %d had %s, want about %s", i+1, d, want[i])
		}
	}
}

func TestClientShouldBeReplacedOnlyWhenAnAttemptGetsNoAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		answer     error
		want       error
		wantCalls  int32
		wantCloses int32
	}{
		// wantCloses is also how many clients are retired: each retired
		// one has its idle connections closed and a new one is built.
		{name: "should not retry or close when the server finds the record", want: nil, wantCalls: 1},
		{name: "should not retry or close when the server says nss_not_found", answer: &api.NSSError{StatusCode: 404, ErrorCode: apitypes.ErrorCodeNSSNotFound}, want: ErrNotFound, wantCalls: 1},
		{name: "should not retry or close when the server says nss_bad_request", answer: &api.NSSError{StatusCode: 400, ErrorCode: apitypes.ErrorCodeNSSBadRequest}, want: ErrInvalid, wantCalls: 1},
		{name: "should not retry or close when the server answers 401", answer: &api.NSSError{StatusCode: 401}, want: ErrUnauthorized, wantCalls: 1},
		{name: "should not retry or close when the server answers 429", answer: &api.NSSError{StatusCode: 429, RetryAfter: time.Minute}, want: ErrRateLimited, wantCalls: 1},
		{name: "should not retry or close when the server answers 503", answer: &api.NSSError{StatusCode: 503}, want: ErrOffline, wantCalls: 1},
		{name: "should retry and close after every attempt when the transport fails", answer: errTransport, want: ErrOffline, wantCalls: 4, wantCloses: 4},
		{name: "should retry and close after every attempt when each one times out", answer: context.DeadlineExceeded, want: ErrOffline, wantCalls: 4, wantCloses: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, nil)
			h.setAnswer(func(string) (*apitypes.NSSUserResponse, error) {
				if tt.answer != nil {
					return nil, tt.answer
				}
				return &apitypes.NSSUserResponse{Name: "alice"}, nil
			})
			_, err := h.client.UserByName(t.Context(), "alice")
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if got := h.calls.Load(); got != tt.wantCalls {
				t.Errorf("got %d calls, want %d", got, tt.wantCalls)
			}
			if got := h.closes.Load(); got != tt.wantCloses {
				t.Errorf("got %d idle closes, want %d", got, tt.wantCloses)
			}
			h.builtMu.Lock()
			built := len(h.built)
			h.builtMu.Unlock()
			// One client to start, and one more for each attempt after a
			// retired one.
			if want := 1 + min(int(tt.wantCloses), int(tt.wantCalls)-1); built != want {
				t.Errorf("built %d clients, want %d", built, want)
			}
		})
	}
}

// TestBlackHoledConnection is the field failure end to end through the
// real HTTPS client: an HTTP/2 server behind a proxy that, after a first
// lookup, silently drops everything the host sends on the kept-alive
// connection. The next lookup's first attempt is written into the dead
// connection; it must give up in time for a retry on a fresh connection
// to answer inside both the timeout and the userdb answer deadline.
func TestBlackHoledConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		timeout        time.Duration
		answerDeadline time.Duration
	}{
		{name: "should answer from a fresh connection inside the answer deadline when the defaults apply", timeout: 5 * time.Second, answerDeadline: 2500 * time.Millisecond},
		{name: "should answer from a fresh connection inside the timeout when both are shorter than the defaults", timeout: time.Second, answerDeadline: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"name":"alice","uid":5001,"gid":5001,"groups":[],"gecos":"","status":"active","key_id":"nss_k1"}}`))
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			t.Cleanup(server.Close)
			proxy, err := blackhole.Start(server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = proxy.Close() })

			keyPath := filepath.Join(t.TempDir(), "nss.key")
			writeKey(t, keyPath, "nss_k1", "secret1")
			sum := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
			cfg := config.Defaults()
			cfg.Server = "https://" + proxy.Addr()
			cfg.TLSSPKIPin = []string{base64.StdEncoding.EncodeToString(sum[:])}
			cfg.APIKeyFile = keyPath
			cfg.Timeout, cfg.SocketAnswerDeadline = tt.timeout, tt.answerDeadline
			client, err := New(Options{Config: &cfg, Log: slog.New(slog.DiscardHandler)})
			if err != nil {
				t.Fatal(err)
			}

			if _, err := client.UserByName(t.Context(), "alice"); err != nil {
				t.Fatalf("first lookup: %v", err)
			}
			proxy.Drop(blackhole.Upstream)

			start := time.Now()
			u, err := client.UserByName(t.Context(), "alice")
			elapsed := time.Since(start)

			if err != nil || u.Name != "alice" {
				t.Fatalf("lookup after the black hole: %+v, %v", u, err)
			}
			if bound := min(tt.timeout, tt.answerDeadline); elapsed >= bound {
				t.Errorf("lookup took %s, want under %s", elapsed, bound)
			}
			if got := proxy.Accepted(); got != 2 {
				t.Errorf("proxy accepted %d connections, want a second, fresh one", got)
			}
			if got := requests.Load(); got != 2 {
				t.Errorf("server saw %d requests, want 2: the black-holed one never arrived", got)
			}
			if st := client.Status().State; st != StateOnline {
				t.Errorf("state %q, want online", st)
			}
		})
	}
}
