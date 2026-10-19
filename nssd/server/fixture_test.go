package server

import (
	"bytes"
	"context"
	"encoding/json"
	"hash/fnv"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/varlink"
	"github.com/mnestor/ssoossh/nssd/cache"
	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/upstream"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

// fakeNSS is the HTTPS API, faked below internal/api so the real upstream
// client, concurrency cap included, runs on top of it.
type fakeNSS struct {
	mu    sync.Mutex
	calls []string
	block chan struct{}
}

// blockCalls makes every call wait until the returned func is called.
func (f *fakeNSS) blockCalls() func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block = make(chan struct{})
	ch := f.block
	return func() { close(ch) }
}

// record notes a call and waits while blocked.
func (f *fakeNSS) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
}

// count returns how many calls were made.
func (f *fakeNSS) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// GetNSSUserByName implements api.NSSClient: every name exists.
func (f *fakeNSS) GetNSSUserByName(_ context.Context, name string) (*apitypes.NSSUserResponse, error) {
	f.record("user/" + name)
	home, shell := "/home/"+name, "/bin/sh"
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	uid := 6000 + h.Sum32()%30000
	if name == "alice" {
		uid = 5001
	}
	return &apitypes.NSSUserResponse{Name: name, UID: uid, GID: uid, Groups: []apitypes.NSSMembership{{GID: 5100, Name: "devs"}},
		Home: &home, Shell: &shell, Gecos: name, Status: apitypes.NSSStatusActive, KeyID: "nss_k"}, nil
}

// GetNSSUserByUID implements api.NSSClient: no uid exists.
func (f *fakeNSS) GetNSSUserByUID(context.Context, uint32) (*apitypes.NSSUserResponse, error) {
	f.record("uid")
	return nil, &api.NSSError{StatusCode: 404, ErrorCode: apitypes.ErrorCodeNSSNotFound}
}

// GetNSSGroupByName implements api.NSSClient.
func (f *fakeNSS) GetNSSGroupByName(_ context.Context, name string) (*apitypes.NSSGroupResponse, error) {
	f.record("group/" + name)
	return &apitypes.NSSGroupResponse{Name: name, GID: 5100, Members: []string{"alice"}}, nil
}

// GetNSSGroupByGID implements api.NSSClient.
func (f *fakeNSS) GetNSSGroupByGID(context.Context, uint32) (*apitypes.NSSGroupResponse, error) {
	f.record("gid")
	return nil, &api.NSSError{StatusCode: 404, ErrorCode: apitypes.ErrorCodeNSSNotFound}
}

// fakePeers is a PeerInspector reporting fixed credentials and cgroup.
type fakePeers struct {
	cred   Credentials
	cgroup string
	err    error
}

// Credentials implements PeerInspector.
func (f fakePeers) Credentials(net.Conn) (Credentials, error) { return f.cred, f.err }

// Cgroup implements PeerInspector.
func (f fakePeers) Cgroup(int32) ([]byte, error) {
	if f.cgroup == "" {
		return nil, os.ErrPermission
	}
	return []byte(f.cgroup), nil
}

// syncBuffer is a log sink safe for concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns what was written.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeClock is a settable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now implements cache.Clock.
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

// fixture is a running Server over a real Daemon and upstream client.
type fixture struct {
	srv    *Server
	daemon *userdb.Daemon
	nss    *fakeNSS
	clock  *fakeClock
	socket string
	admin  string
	logs   *syncBuffer
	cancel context.CancelFunc
	done   chan struct{}
}

// socketDir returns a directory short enough for AF_UNIX's 108-byte path
// limit: t.TempDir() when it fits, else one under /tmp.
func socketDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if len(dir) < 80 {
		return dir
	}
	dir, err := os.MkdirTemp("/tmp", "nssd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// fixtureReadTimeout is how long the fixture's server waits for each
// call: short, so the read-timeout tests stay quick.
const fixtureReadTimeout = 100 * time.Millisecond

// start builds and runs a server; mutate adjusts the configuration and
// peers sets the peer inspector (the real one when nil).
func start(t *testing.T, peers PeerInspector, mutate func(*config.Config)) *fixture {
	t.Helper()
	return startWithReadTimeout(t, peers, mutate, fixtureReadTimeout)
}

// startWithReadTimeout is start with the server's per-call read timeout
// set, for a test that must not be cut by it.
func startWithReadTimeout(t *testing.T, peers PeerInspector, mutate func(*config.Config), readTimeout time.Duration) *fixture {
	t.Helper()
	dir := socketDir(t)
	files := map[string]string{
		"passwd":  "root:x:0:0::/root:/bin/sh\nbob:x:1001:1001::/home/bob:/bin/sh\n",
		"group":   "root:x:0:\n",
		"shells":  "/bin/sh\n",
		"nss.key": "nss_k\nsecret\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.Server = "https://sso.example.com"
	cfg.TLSSPKIPin = []string{"AAAA"}
	cfg.APIKeyFile = filepath.Join(dir, "nss.key")
	cfg.SocketAnswerDeadline = 300 * time.Millisecond
	cfg.LoginRefreshTimeout = 200 * time.Millisecond
	cfg.RefreshRate = 1000
	cfg.CacheMode = config.CacheModeMemory
	if mutate != nil {
		mutate(&cfg)
	}

	f := &fixture{nss: &fakeNSS{}, clock: &fakeClock{now: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)}, logs: &syncBuffer{}, done: make(chan struct{})}
	log := slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	up, err := upstream.New(upstream.Options{
		Config:    &cfg,
		Log:       log,
		NewClient: func(api.Config) (api.NSSClient, error) { return f.nss, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	pol := policy.New(&cfg, policy.Paths{
		Passwd: filepath.Join(dir, "passwd"), Group: filepath.Join(dir, "group"),
		Shells: filepath.Join(dir, "shells"), Overrides: filepath.Join(dir, "overrides.yaml"),
	}, log)
	f.daemon = userdb.New(userdb.Options{Config: &cfg, Store: cache.NewMemoryStore(), Upstream: up, Policy: pol, Clock: f.clock, Log: log})
	if peers == nil {
		peers = ProcInspector{}
	}
	f.srv = New(Options{
		Daemon: f.daemon, Peers: peers, SSHDUnits: cfg.SSHDUnits, AnswerDeadline: cfg.SocketAnswerDeadline,
		ReadTimeout: readTimeout, MaxConnections: cfg.SocketMaxConnections, IdleTimeout: cfg.SocketIdleTimeout,
		Now: f.clock.Now, Log: log,
	})

	f.socket = filepath.Join(dir, "userdb.sock")
	f.admin = filepath.Join(dir, "run", "admin.sock")
	userdbL, err := Listen(f.socket, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	adminL, err := ListenAdmin(f.admin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	var wg sync.WaitGroup
	wg.Go(func() { f.daemon.Run(ctx) })
	wg.Go(func() {
		if err := f.srv.Serve(ctx, userdbL); err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	wg.Go(func() {
		if err := f.srv.ServeAdmin(ctx, adminL); err != nil {
			t.Errorf("serve admin: %v", err)
		}
	})
	go func() { wg.Wait(); close(f.done) }()
	t.Cleanup(f.stop)
	return f
}

// stop shuts the server down and waits for it.
func (f *fixture) stop() {
	f.cancel()
	<-f.done
}

// dial opens a varlink client on the userdb socket.
func (f *fixture) dial(t *testing.T) *varlink.Client {
	t.Helper()
	c, err := varlink.Dial(t.Context(), f.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// params is a userdb parameter object with the service set.
func params(kv ...any) map[string]any {
	m := map[string]any{"service": userdb.ServiceName}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// recordOf decodes a record reply's record.
func recordOf(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out struct {
		Record map[string]any `json:"record"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.Record
}
