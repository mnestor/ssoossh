package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
)

const (
	caKeyOne = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJirRcsGXT31qUGNbgTkbI6sxq1SbSLN++XEr705S8ko ca-one@example"
	caKeyTwo = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINlbdxKlyGGaGRLcaOWWJcRJUdcVJEIvA0SBCVSbdcxJ ca-two@example"
)

// caStub answers GetCA however a test needs and counts the calls, which is
// how "used the cache" is asserted: the cache is only doing its job if the
// server is not asked at all.
type caStub struct {
	*fakeAPIClient
	response string
	err      error
	calls    int
}

func (c *caStub) GetCA(context.Context) (string, error) {
	c.calls++
	if c.err != nil {
		return "", c.err
	}
	return c.response, nil
}

func newCAStub(response string, err error) *caStub {
	return &caStub{fakeAPIClient: &fakeAPIClient{}, response: response, err: err}
}

// writeCache puts a cache file in place with a chosen age, so a test can
// say "cached yesterday" without waiting a day.
func writeCache(t *testing.T, dir string, keys []string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, "ca.json")
	data, err := json.Marshal(caCache{Keys: keys, FetchedAt: time.Now().Add(-age)})
	if err != nil {
		t.Fatalf("encode cache: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	return path
}

// readCache is the assertion half of writeCache.
func readCache(t *testing.T, path string) caCache {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var c caCache
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse cache: %v", err)
	}
	return c
}

func TestResolveCAKeys_ShouldNotContactTheServerWhenTheCacheIsFresh(t *testing.T) {
	t.Parallel()

	path := writeCache(t, t.TempDir(), []string{caKeyOne}, time.Hour)
	stub := newCAStub(caKeyTwo, nil)

	keys, err := resolveCAKeys(t.Context(), stub, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("called the server %d times, want 0", stub.calls)
	}
	if len(keys) != 1 || keys[0] != caKeyOne {
		t.Errorf("got %v, want the cached key", keys)
	}
}

// The regression this whole feature exists for: a client holding a usable
// key must not fail because the server is down.
func TestResolveCAKeys_ShouldKeepTheCachedKeysWhenTheServerIsUnreachable(t *testing.T) {
	t.Parallel()

	path := writeCache(t, t.TempDir(), []string{caKeyOne}, 48*time.Hour)
	stub := newCAStub("", errors.New("connection refused"))

	keys, err := resolveCAKeys(t.Context(), stub, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0] != caKeyOne {
		t.Errorf("got %v, want the cached key", keys)
	}
}

func TestResolveCAKeys_ShouldRefreshWhenTheCacheIsStale(t *testing.T) {
	t.Parallel()

	path := writeCache(t, t.TempDir(), []string{caKeyOne}, 48*time.Hour)
	stub := newCAStub(caKeyTwo, nil)

	keys, err := resolveCAKeys(t.Context(), stub, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0] != caKeyTwo {
		t.Fatalf("got %v, want the freshly fetched key", keys)
	}
	if got := readCache(t, path); len(got.Keys) != 1 || got.Keys[0] != caKeyTwo {
		t.Errorf("cache holds %v, want the freshly fetched key", got.Keys)
	}
}

func TestResolveCAKeys_ShouldFailWhenThereIsNothingCachedAndTheServerIsUnreachable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ca.json")
	stub := newCAStub("", errors.New("connection refused"))

	if _, err := resolveCAKeys(t.Context(), stub, path); err == nil {
		t.Fatal("expected an error with no cache and no server")
	}
}

func TestResolveCAKeys_ShouldFailWhenTheServerReturnsNoKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ca.json")
	stub := newCAStub("", nil)

	if _, err := resolveCAKeys(t.Context(), stub, path); err == nil {
		t.Fatal("expected an error when the server returns no CA key")
	}
}

func TestResolveCAKeys_ShouldKeepTheCachedKeysWhenTheServerReturnsNone(t *testing.T) {
	t.Parallel()

	path := writeCache(t, t.TempDir(), []string{caKeyOne}, 48*time.Hour)
	stub := newCAStub("   \n\n", nil)

	keys, err := resolveCAKeys(t.Context(), stub, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0] != caKeyOne {
		t.Errorf("got %v, want the cached key", keys)
	}
}

// A rotation: the server announces both keys at once, and the client has to
// take both or it rejects half the certificates issued in the changeover.
func TestResolveCAKeys_ShouldCacheEveryKeyTheServerReturns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ca.json")
	stub := newCAStub(caKeyOne+"\n"+caKeyTwo, nil)

	keys, err := resolveCAKeys(t.Context(), stub, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2: %v", len(keys), keys)
	}
	if got := readCache(t, path); len(got.Keys) != 2 {
		t.Errorf("cache holds %d keys, want 2: %v", len(got.Keys), got.Keys)
	}
}

func TestResolveCAKeys_ShouldTreatAnUnreadableCacheAsAbsent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
	}{
		{name: "should refetch when the cache is not JSON", contents: "{not json"},
		{name: "should refetch when the cache holds no keys", contents: `{"keys":[],"fetched_at":"2126-01-01T00:00:00Z"}`},
		{name: "should refetch when the cache is empty", contents: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "ca.json")
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatalf("write cache: %v", err)
			}
			stub := newCAStub(caKeyTwo, nil)

			keys, err := resolveCAKeys(t.Context(), stub, path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if stub.calls != 1 {
				t.Errorf("called the server %d times, want 1", stub.calls)
			}
			if len(keys) != 1 || keys[0] != caKeyTwo {
				t.Errorf("got %v, want the freshly fetched key", keys)
			}
		})
	}
}

// A cache stamped in the future is a clock that moved, not a fresh cache.
func TestResolveCAKeys_ShouldRefreshWhenTheCacheIsStampedInTheFuture(t *testing.T) {
	t.Parallel()

	path := writeCache(t, t.TempDir(), []string{caKeyOne}, -time.Hour)
	stub := newCAStub(caKeyTwo, nil)

	keys, err := resolveCAKeys(t.Context(), stub, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0] != caKeyTwo {
		t.Errorf("got %v, want the freshly fetched key", keys)
	}
}

// No path means no cache, which is what an unset HOME produces. It must
// behave exactly as the client did before there was a cache at all.
func TestResolveCAKeys_ShouldFetchEveryTimeWhenThereIsNoCachePath(t *testing.T) {
	t.Parallel()

	stub := newCAStub(caKeyOne, nil)

	for range 2 {
		if _, err := resolveCAKeys(t.Context(), stub, ""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if stub.calls != 2 {
		t.Errorf("called the server %d times, want 2", stub.calls)
	}
}

// Whoever can rewrite this file chooses which certificates the client
// recognizes.
func TestSaveCACache_ShouldWriteAPrivateFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode bits; the Windows half of fileperm rewrites an ACL instead")
	}
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "ca.json")
	saveCACache(path, []string{caKeyOne})

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache mode is %04o, want 0600", perm)
	}
}

func TestSaveCACache_ShouldDoNothingWithoutAPathOrKeys(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "ca.json")

	saveCACache("", []string{caKeyOne})
	saveCACache(path, nil)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cache file exists, want none written")
	}
}

// The warning is the only thing that distinguishes a rotation from a server
// rebuilt around a different CA, so which cases produce one matters.
//
// Not parallel: it swaps the process-wide slog default.
func TestWarnOnCAReplacement(t *testing.T) {
	tests := []struct {
		name     string
		cached   []string
		fetched  []string
		wantWarn bool
	}{
		{
			name:     "should warn when the new set shares nothing with the cached one",
			cached:   []string{caKeyOne},
			fetched:  []string{caKeyTwo},
			wantWarn: true,
		},
		{
			name:     "should stay silent when a rotation adds a key",
			cached:   []string{caKeyOne},
			fetched:  []string{caKeyOne, caKeyTwo},
			wantWarn: false,
		},
		{
			name:     "should stay silent when a rotation retires the old key",
			cached:   []string{caKeyOne, caKeyTwo},
			fetched:  []string{caKeyTwo},
			wantWarn: false,
		},
		{
			name:     "should stay silent when nothing changed",
			cached:   []string{caKeyOne},
			fetched:  []string{caKeyOne},
			wantWarn: false,
		},
		{
			name:     "should stay silent on the first fetch, with nothing cached",
			cached:   nil,
			fetched:  []string{caKeyOne},
			wantWarn: false,
		},
		{
			name:     "should stay silent rather than cry replacement over a key that will not parse",
			cached:   []string{"not-a-key"},
			fetched:  []string{caKeyOne},
			wantWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(previous) })

			warnOnCAReplacement(tt.cached, tt.fetched)

			warned := bytes.Contains(buf.Bytes(), []byte("replaced entirely"))
			if warned != tt.wantWarn {
				t.Errorf("warned=%v, want %v; log was %q", warned, tt.wantWarn, buf.String())
			}
		})
	}
}

// The wiring, not just the resolution: PreRun has to consult the cache
// seam, or the whole feature is unreachable from a real invocation. This is
// the failure the cache exists to remove — before it, a client with a
// perfectly good certificate could not get past startup while the server
// was down, for a CA key it already had on disk.
func TestRootCommandPreRun_ShouldStartFromTheCachedCAWhenTheServerIsDown(t *testing.T) {
	t.Parallel()

	path := writeCache(t, t.TempDir(), []string{caKeyOne}, time.Hour)
	stub := newCAStub("", errors.New("connection refused"))

	root := &RootCommand{
		newConfig:    func(*cobra.Command) (*config.Config, error) { return &config.Config{UseAgent: true}, nil },
		newAPIClient: func(*config.Config) (api.Client, error) { return stub, nil },
		newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
		newFileAgent: func(string) (agent.Agent, error) { return &fakeAgent{}, nil },
		caCachePath:  func() string { return path },
	}

	cd := &simplecobra.Commandeer{CobraCommand: &cobra.Command{}}
	if err := root.PreRun(cd, cd); err != nil {
		t.Fatalf("PreRun returned an error: %v", err)
	}
	if root.InitErr() != nil {
		t.Fatalf("InitErr = %v, want nil: the cached CA key should have carried startup", root.InitErr())
	}

	cfg := root.Config()
	if len(cfg.CAPubkey) != 1 || cfg.CAPubkey[0] != caKeyOne {
		t.Errorf("CAPubkey = %v, want the cached key", cfg.CAPubkey)
	}
	// A cached key is a fetched key. Reporting it to the server as pinned
	// would claim a trust anchor no operator set.
	if cfg.CAPubkeyPinned {
		t.Error("CAPubkeyPinned is true for a cached key, want false")
	}
}

// A pinned key must short-circuit the cache entirely: no cache read, no
// fetch, and it is the one kind of key worth reporting to the server.
func TestRootCommandPreRun_ShouldNotConsultTheServerOrCacheWhenCapubkeyIsPinned(t *testing.T) {
	t.Parallel()

	stub := newCAStub(caKeyTwo, nil)
	cachePathCalls := 0

	root := &RootCommand{
		newConfig: func(*cobra.Command) (*config.Config, error) {
			return &config.Config{UseAgent: true, CAPubkey: []string{caKeyOne}}, nil
		},
		newAPIClient: func(*config.Config) (api.Client, error) { return stub, nil },
		newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
		newFileAgent: func(string) (agent.Agent, error) { return &fakeAgent{}, nil },
		caCachePath:  func() string { cachePathCalls++; return "" },
	}

	cd := &simplecobra.Commandeer{CobraCommand: &cobra.Command{}}
	if err := root.PreRun(cd, cd); err != nil {
		t.Fatalf("PreRun returned an error: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("called the server %d times, want 0", stub.calls)
	}
	if cachePathCalls != 0 {
		t.Errorf("resolved the cache path %d times, want 0", cachePathCalls)
	}
	if !root.Config().CAPubkeyPinned {
		t.Error("CAPubkeyPinned is false for a configured key, want true")
	}
}
