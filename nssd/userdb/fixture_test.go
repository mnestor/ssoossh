package userdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/varlink"
	"github.com/mnestor/ssoossh/nssd/cache"
	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/upstream"
)

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

// fakeUpstream answers from maps and records every call.
type fakeUpstream struct {
	mu     sync.Mutex
	users  map[string]*apitypes.NSSUserResponse
	groups map[string]*apitypes.NSSGroupResponse
	err    error
	calls  []string
	block  chan struct{}
	resets int
	status upstream.Status
}

// newFakeUpstream returns a directory holding alice, a disabled carol, a
// deleted dave and the group devs.
func newFakeUpstream() *fakeUpstream {
	return &fakeUpstream{
		users: map[string]*apitypes.NSSUserResponse{
			"alice": userResp("alice", 5001, apitypes.NSSStatusActive),
			"carol": userResp("carol", 5003, apitypes.NSSStatusDisabled),
			"dave":  userResp("dave", 5004, apitypes.NSSStatusDeleted),
		},
		groups: map[string]*apitypes.NSSGroupResponse{
			"devs": {Name: "devs", GID: 5100, Members: []string{"alice", "carol"}},
			"solo": {Name: "solo", GID: 5101, Members: []string{"alice"}},
			"none": {Name: "none", GID: 5102, Members: []string{}},
		},
		status: upstream.Status{State: upstream.StateOnline, KeyID: "nss_k"},
	}
}

// userResp is a server user answer.
func userResp(name string, uid uint32, status string) *apitypes.NSSUserResponse {
	home, shell := "/home/"+name, "/bin/bash"
	return &apitypes.NSSUserResponse{
		Name: name, UID: uid, GID: uid,
		Groups: []apitypes.NSSMembership{{GID: uid, Name: name}, {GID: 5100, Name: "devs"}, {GID: 27, Name: "sudo"}},
		Home:   &home, Shell: &shell, Gecos: "User: " + name, Status: status, KeyID: "nss_k",
	}
}

// record notes a call and waits while blocked, reporting the error to
// answer with, if any.
func (f *fakeUpstream) record(call string) error {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	block, err := f.block, f.err
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return err
}

// UserByName implements upstream.Upstream.
func (f *fakeUpstream) UserByName(_ context.Context, name string) (*apitypes.NSSUserResponse, error) {
	if err := f.record("user/name/" + name); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[name]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, upstream.ErrNotFound
}

// UserByUID implements upstream.Upstream.
func (f *fakeUpstream) UserByUID(_ context.Context, uid uint32) (*apitypes.NSSUserResponse, error) {
	if err := f.record("user/uid"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.UID == uid {
			cp := *u
			return &cp, nil
		}
	}
	return nil, upstream.ErrNotFound
}

// GroupByName implements upstream.Upstream.
func (f *fakeUpstream) GroupByName(_ context.Context, name string) (*apitypes.NSSGroupResponse, error) {
	if err := f.record("group/name/" + name); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.groups[name]; ok {
		cp := *g
		return &cp, nil
	}
	return nil, upstream.ErrNotFound
}

// GroupByGID implements upstream.Upstream.
func (f *fakeUpstream) GroupByGID(_ context.Context, gid uint32) (*apitypes.NSSGroupResponse, error) {
	if err := f.record("group/gid"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.groups {
		if g.GID == gid {
			cp := *g
			return &cp, nil
		}
	}
	return nil, upstream.ErrNotFound
}

// Status implements upstream.Upstream.
func (f *fakeUpstream) Status() upstream.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

// Reset implements upstream.Upstream.
func (f *fakeUpstream) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets++
}

// setErr makes every call fail with err (nil to stop).
func (f *fakeUpstream) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// callCount returns how many upstream calls were made.
func (f *fakeUpstream) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fixture is a Daemon over fakes.
type fixture struct {
	d     *Daemon
	up    *fakeUpstream
	clock *fakeClock
	store cache.Store
	cfg   *config.Config
	paths policy.Paths
	logs  *syncBuffer
}

// syncBuffer is a bytes.Buffer safe for the logger's concurrent writes.
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

// newFixture builds a Daemon with a memory store; mutate adjusts the
// configuration first.
func newFixture(t *testing.T, mutate func(*config.Config)) *fixture {
	t.Helper()
	dir := t.TempDir()
	paths := policy.Paths{
		Passwd:    filepath.Join(dir, "passwd"),
		Group:     filepath.Join(dir, "group"),
		Shells:    filepath.Join(dir, "shells"),
		Overrides: filepath.Join(dir, "nss-overrides.yaml"),
	}
	files := map[string]string{
		paths.Passwd: "root:x:0:0::/root:/bin/bash\nbob:x:1001:1001::/home/bob:/bin/sh\n",
		paths.Group:  "root:x:0:\nsudo:x:27:\n",
		paths.Shells: "/bin/sh\n/bin/bash\n",
	}
	for p, content := range files {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.Server = "https://sso.example.com"
	if mutate != nil {
		mutate(&cfg)
	}
	f := &fixture{
		up:    newFakeUpstream(),
		clock: &fakeClock{now: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)},
		store: cache.NewMemoryStore(),
		cfg:   &cfg,
		paths: paths,
		logs:  &syncBuffer{},
	}
	log := slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	f.d = New(Options{
		Config:   &cfg,
		Store:    f.store,
		Upstream: f.up,
		Policy:   policy.New(&cfg, paths, log),
		Clock:    f.clock,
		Log:      log,
	})
	return f
}

// call runs one call through Handle and returns the replies written.
func (f *fixture) call(t *testing.T, method string, params any, peer Peer, more bool) []varlink.Reply {
	t.Helper()
	return f.callCtx(t, t.Context(), varlink.Call{Method: method, Parameters: mustJSON(t, params), More: more}, peer)
}

// callCtx is call with a context and a ready-made call.
func (f *fixture) callCtx(t *testing.T, ctx context.Context, c varlink.Call, peer Peer) []varlink.Reply {
	t.Helper()
	var out bytes.Buffer
	conn := varlink.NewServerConn(struct {
		io.Reader
		io.Writer
	}{bytes.NewReader(nil), &out})
	if err := f.d.Handle(ctx, c, peer, conn); err != nil {
		t.Fatalf("handle: %v", err)
	}
	dec := varlink.NewDecoder(&out)
	var replies []varlink.Reply
	for {
		r, err := dec.ReadReply()
		if errors.Is(err, io.EOF) {
			return replies
		}
		if err != nil {
			t.Fatalf("decode reply: %v", err)
		}
		replies = append(replies, r)
	}
}

// mustJSON encodes v.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// one returns the only reply, failing otherwise.
func one(t *testing.T, replies []varlink.Reply) varlink.Reply {
	t.Helper()
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want 1: %+v", len(replies), replies)
	}
	return replies[0]
}

// errorOf returns the only reply's error name.
func errorOf(t *testing.T, replies []varlink.Reply) string {
	t.Helper()
	return one(t, replies).Error
}

// p builds a parameter object with the service set.
func p(kv ...any) map[string]any {
	m := map[string]any{"service": ServiceName}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// root and user are the two kinds of peer.
var (
	rootPeer = Peer{UID: 0}
	userPeer = Peer{UID: 1000}
	sshdPeer = Peer{UID: 0, SSHD: true}
)

// decodeRecord decodes a record reply.
func decodeRecord(t *testing.T, r varlink.Reply) (map[string]any, bool) {
	t.Helper()
	if r.Error != "" {
		t.Fatalf("got error %s", r.Error)
	}
	var out struct {
		Record     map[string]any `json:"record"`
		Incomplete bool           `json:"incomplete"`
	}
	if err := json.Unmarshal(r.Parameters, &out); err != nil {
		t.Fatal(err)
	}
	return out.Record, out.Incomplete
}
