package upstream

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/apitypes"
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

// fakeAPI is an api.NSSClient answering from a function, recording calls.
type fakeAPI struct {
	url    string
	key    string
	answer func(url string) (*apitypes.NSSUserResponse, error)
	calls  *atomic.Int32
}

// GetNSSUserByName implements api.NSSClient.
func (f *fakeAPI) GetNSSUserByName(context.Context, string) (*apitypes.NSSUserResponse, error) {
	f.calls.Add(1)
	return f.answer(f.url)
}

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
	client   *Client
	clock    *fakeClock
	calls    atomic.Int32
	keyPath  string
	built    []api.Config
	builtMu  sync.Mutex
	logs     *bytes.Buffer
	answer   func(url string) (*apitypes.NSSUserResponse, error)
	answerMu sync.Mutex
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
			return &fakeAPI{url: c.ServerURL, key: c.KeyID, calls: &h.calls, answer: func(url string) (*apitypes.NSSUserResponse, error) {
				h.answerMu.Lock()
				fn := h.answer
				h.answerMu.Unlock()
				return fn(url)
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
	if b.KeyID != "nss_k1" || b.KeySecret != "secret1" || b.ServerURL != "https://sso.example.com" || b.TLSSPKIPins[0] != "AAAA" || b.RequestTimeout != 5*time.Second {
		t.Fatalf("got api config %+v", b)
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

func TestRetriesShouldRotateThroughDiscoveredServersWhenOneFails(t *testing.T) {
	t.Parallel()
	var discoveries atomic.Int32
	h := newHarness(t, func(c *config.Config, o *Options) {
		c.Server, c.Domain, c.ReconnectionRetries = "", "corp.example", 2
		o.Discover = func(_ context.Context, domain string) ([]string, error) {
			discoveries.Add(1)
			if domain != "corp.example" {
				t.Errorf("discovered %q", domain)
			}
			return []string{"https://a:443", "https://b:443", "https://c:443"}, nil
		}
		o.Sleep = func(context.Context, time.Duration) error {
			t.Error("should not pause when another server is there to try")
			return nil
		}
	})
	var tried []string
	var mu sync.Mutex
	h.setAnswer(func(url string) (*apitypes.NSSUserResponse, error) {
		mu.Lock()
		tried = append(tried, url)
		mu.Unlock()
		if url != "https://c:443" {
			return nil, errTransport
		}
		return &apitypes.NSSUserResponse{Name: "alice"}, nil
	})
	ctx := t.Context()
	if _, err := h.client.UserByName(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(tried, ",") != "https://a:443,https://b:443,https://c:443" {
		t.Fatalf("tried %v", tried)
	}
	// The server that answered goes first next time, with no rediscovery.
	tried = nil
	if _, err := h.client.UserByName(ctx, "alice"); err != nil || len(tried) != 1 || tried[0] != "https://c:443" {
		t.Fatalf("got %v, tried %v", err, tried)
	}
	if h.client.Status().Server != "https://c:443" || discoveries.Load() != 1 {
		t.Fatalf("got status %+v after %d discoveries", h.client.Status(), discoveries.Load())
	}
}

func TestRetriesShouldPauseBetweenAttemptsWhenThereIsOneServer(t *testing.T) {
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

func TestDiscoveryFailureShouldGoOfflineWhenNoServerIsPublished(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(c *config.Config, o *Options) {
		c.Server, c.Domain = "", "corp.example"
		o.Discover = func(context.Context, string) ([]string, error) { return nil, api.ErrNoServerRecords }
	})
	if _, err := h.client.UserByName(t.Context(), "alice"); !errors.Is(err, ErrOffline) || !errors.Is(err, api.ErrNoServerRecords) {
		t.Fatalf("got %v", err)
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
