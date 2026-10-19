package server

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/varlink"
	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/upstream"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

func TestServeShouldAnswerSeveralCallsWhenTheyShareAConnection(t *testing.T) {
	t.Parallel()
	f := start(t, nil, nil)
	c := f.dial(t)
	ctx := t.Context()

	var user json.RawMessage
	if err := c.Call(ctx, userdb.MethodGetUserRecord, params("userName", "alice"), &user); err != nil {
		t.Fatal(err)
	}
	if rec := recordOf(t, user); rec["userName"] != "alice" {
		t.Fatalf("got %v", rec)
	}
	var group json.RawMessage
	if err := c.Call(ctx, userdb.MethodGetGroupRecord, params("groupName", "devs"), &group); err != nil {
		t.Fatal(err)
	}
	var pairs []string
	err := c.CallMore(ctx, userdb.MethodGetMemberships, params("userName", "alice"), func(raw json.RawMessage) error {
		pairs = append(pairs, string(raw))
		return nil
	})
	if err != nil || len(pairs) != 1 {
		t.Fatalf("got %v, %v", pairs, err)
	}
	var verr *varlink.Error
	if err := c.Call(ctx, userdb.MethodGetUserRecord, map[string]any{"userName": "alice"}, nil); !errors.As(err, &verr) || verr.Name != userdb.ErrorBadService {
		t.Fatalf("should answer BadService and keep the connection, got %v", err)
	}
	var info map[string]any
	if err := c.Call(ctx, userdb.MethodGetInfo, nil, &info); err != nil || info["product"] != "ssoossh-nss" {
		t.Fatalf("got %v, %v", info, err)
	}
}

func TestServeShouldMarkTheRecordIncompleteWhenThePeerIsNotRoot(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("the real peer is root here")
	}
	f := start(t, nil, nil)
	var raw json.RawMessage
	if err := f.dial(t).Call(t.Context(), userdb.MethodGetUserRecord, params("userName", "alice"), &raw); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Record     map[string]any `json:"record"`
		Incomplete bool           `json:"incomplete"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || !out.Incomplete || out.Record["privileged"] != nil {
		t.Fatalf("got %s", raw)
	}
}

func TestServeShouldSendPrivilegedDataWhenThePeerIsRoot(t *testing.T) {
	t.Parallel()
	f := start(t, fakePeers{cred: Credentials{UID: 0, PID: 4242}}, nil)
	var raw json.RawMessage
	if err := f.dial(t).Call(t.Context(), userdb.MethodGetUserRecord, params("userName", "alice"), &raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"privileged":{"hashedPassword":["*"]}`) || !strings.Contains(string(raw), `"incomplete":false`) {
		t.Fatalf("got %s", raw)
	}
}

func TestServeShouldCloseTheConnection(t *testing.T) {
	t.Parallel()

	// slowTimers keeps every timer out of the way of a case that writes
	// first: a test descheduled past a 100 ms read timeout before its write
	// would find the connection already closed and fail on EPIPE, and the
	// close it then saw would not be the one the case is about. The
	// client's 5 s read deadline below is what bounds those cases.
	const slowTimers = 30 * time.Second

	tests := []struct {
		name string
		// readTimeout is the server's per-call wait; zero is the
		// fixture's 100 ms.
		readTimeout time.Duration
		// idleTimeout, when set, is socket_idle_timeout.
		idleTimeout time.Duration
		send        func(t *testing.T, conn net.Conn)
	}{
		{name: "should close when no call arrives within the read timeout", send: func(*testing.T, net.Conn) {}},
		{name: "should close when the call is not JSON", readTimeout: slowTimers, send: func(t *testing.T, conn net.Conn) { write(t, conn, []byte("not json\x00")) }},
		// One buffer past the limit with no NUL: the decoder notices an
		// oversize message a buffer at a time, so a couple of bytes over
		// would sit waiting for more until the read timeout, and the case
		// would pass on the timer instead of the limit.
		{name: "should close when a message passes 64 KiB", readTimeout: slowTimers, send: func(t *testing.T, conn net.Conn) {
			write(t, conn, bytes.Repeat([]byte("a"), varlink.MaxMessageSize+4096))
		}},
		{name: "should close when a call has no method", readTimeout: slowTimers, send: func(t *testing.T, conn net.Conn) { write(t, conn, []byte(`{"parameters":{}}`+"\x00")) }},
		{name: "should close after a call when the connection then sits idle", readTimeout: slowTimers, idleTimeout: time.Second, send: func(t *testing.T, conn net.Conn) {
			write(t, conn, []byte(`{"method":"org.varlink.service.GetInfo"}`+"\x00"))
			if _, err := varlink.NewDecoder(conn).ReadReply(); err != nil {
				t.Errorf("first reply: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			readTimeout := cmp.Or(tt.readTimeout, fixtureReadTimeout)
			var mutate func(*config.Config)
			if tt.idleTimeout > 0 {
				mutate = func(c *config.Config) { c.SocketIdleTimeout = tt.idleTimeout }
			}
			f := startWithReadTimeout(t, nil, mutate, readTimeout)
			conn, err := net.Dial("unix", f.socket)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			tt.send(t, conn)
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, conn); err != nil {
				t.Fatalf("should close the connection, the read ended with %v", err)
			}
		})
	}
}

// write sends b on conn.
func write(t *testing.T, conn net.Conn, b []byte) {
	t.Helper()
	if _, err := conn.Write(b); err != nil {
		t.Fatal(err)
	}
}

func TestServeShouldAnswerServiceNotAvailableWhenUpstreamIsSlow(t *testing.T) {
	t.Parallel()
	f := start(t, nil, nil)
	release := f.nss.blockCalls()
	defer release()

	start := time.Now()
	var verr *varlink.Error
	err := f.dial(t).Call(t.Context(), userdb.MethodGetUserRecord, params("userName", "alice"), nil)
	elapsed := time.Since(start)
	if !errors.As(err, &verr) || verr.Name != userdb.ErrorServiceNotAvailable {
		t.Fatalf("got %v", err)
	}
	// Answered at the 300ms deadline, not after upstream (which is held
	// until the test ends); the upper bound leaves room for a loaded CI.
	if elapsed > time.Second || elapsed < 150*time.Millisecond {
		t.Fatalf("should answer at socket_answer_deadline (300ms), took %v", elapsed)
	}
}

func TestServeShouldAnswerACallInFlightWhenShutdownBegins(t *testing.T) {
	t.Parallel()
	f := start(t, nil, func(c *config.Config) { c.SocketAnswerDeadline = 2 * time.Second })
	release := f.nss.blockCalls()

	done := make(chan error, 1)
	var reply json.RawMessage
	c := f.dial(t)
	go func() {
		done <- c.Call(context.Background(), userdb.MethodGetUserRecord, params("userName", "alice"), &reply)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for f.nss.count() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the lookup never reached upstream")
		}
		time.Sleep(time.Millisecond)
	}

	// Shutdown begins while the call waits on upstream, which then answers.
	f.cancel()
	release()

	if err := <-done; err != nil {
		t.Fatalf("Call() error = %v, want the record answered despite the shutdown", err)
	}
	if got := recordOf(t, reply)["userName"]; got != "alice" {
		t.Fatalf("userName = %v, want alice", got)
	}
}

func TestServeShouldRefuseAtOnceWhenTheUpstreamCapIsFull(t *testing.T) {
	t.Parallel()
	f := start(t, nil, func(c *config.Config) { c.SocketAnswerDeadline = 2 * time.Second; c.LoginRefreshTimeout = time.Second })
	release := f.nss.blockCalls()

	// Eight cold misses for distinct names fill the cap and block.
	var wg sync.WaitGroup
	for i := range upstream.MaxInFlight {
		wg.Go(func() {
			c, err := varlink.Dial(context.Background(), f.socket)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = c.Close() }()
			_ = c.Call(context.Background(), userdb.MethodGetUserRecord, params("userName", fmt.Sprintf("user%02d", i)), nil)
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.nss.count() < upstream.MaxInFlight {
		if time.Now().After(deadline) {
			t.Fatalf("only %d lookups reached upstream", f.nss.count())
		}
		time.Sleep(time.Millisecond)
	}

	start := time.Now()
	var verr *varlink.Error
	err := f.dial(t).Call(t.Context(), userdb.MethodGetUserRecord, params("userName", "overflow"), nil)
	if !errors.As(err, &verr) || verr.Name != userdb.ErrorServiceNotAvailable {
		t.Fatalf("got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("should refuse over the cap at once, not at the 2s deadline; took %v", elapsed)
	}
	release()
	wg.Wait()
}

func TestServeShouldRefreshForSSHDWhenItsCgroupIsSSHDService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		peers     fakePeers
		wantCalls int
	}{
		{name: "should refresh when the peer is root in sshd.service", peers: fakePeers{cgroup: "0::/system.slice/sshd.service\n"}, wantCalls: 2},
		{name: "should refresh when the peer is root in an ssh@ instance", peers: fakePeers{cgroup: "0::/system.slice/system-ssh.slice/ssh@3-10.0.0.1:22-10.0.0.2:5555.service\n"}, wantCalls: 2},
		{name: "should not refresh when the root peer is in a session scope", peers: fakePeers{cgroup: "0::/user.slice/user-0.slice/session-4.scope\n"}, wantCalls: 1},
		{name: "should not refresh when the root peer's cgroup is unreadable", peers: fakePeers{}, wantCalls: 1},
		{name: "should not refresh when sshd.service runs as another uid", peers: fakePeers{cred: Credentials{UID: 74}, cgroup: "0::/system.slice/sshd.service\n"}, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := start(t, tt.peers, nil)
			c := f.dial(t)
			if err := c.Call(t.Context(), userdb.MethodGetUserRecord, params("userName", "alice"), nil); err != nil {
				t.Fatal(err)
			}
			f.clock.Advance(2 * time.Minute) // past login_soft_ttl, short of nowait
			if err := c.Call(t.Context(), userdb.MethodGetUserRecord, params("userName", "alice"), nil); err != nil {
				t.Fatal(err)
			}
			if n := f.nss.count(); n != tt.wantCalls {
				t.Fatalf("made %d upstream calls, want %d", n, tt.wantCalls)
			}
		})
	}
}

func TestServeShouldDebounceRefreshUserWhenItRepeats(t *testing.T) {
	t.Parallel()
	f := start(t, nil, nil)
	c := f.dial(t)
	for range 5 {
		if err := c.CallOneway(t.Context(), userdb.MethodRefreshUser, map[string]any{"userName": "alice"}); err != nil {
			t.Fatal(err)
		}
	}
	// A call after the one-way ones proves they were all read.
	if err := c.Call(t.Context(), userdb.MethodGetInfo, nil, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.nss.count() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the refresh never ran")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // room for a wrongly queued second refresh to show
	if n := f.nss.count(); n != 1 {
		t.Fatalf("made %d upstream calls for five RefreshUser calls", n)
	}
}

func TestServeShouldCloseWhenPeerCredentialsFail(t *testing.T) {
	t.Parallel()
	f := start(t, fakePeers{err: errors.New("no creds")}, nil)
	conn, err := net.Dial("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.logs.String(), "cannot read a userdb peer's credentials") {
		t.Fatalf("got logs %s", f.logs)
	}
}

func TestServeShouldCloseWhenTheReplyCannotBeWritten(t *testing.T) {
	t.Parallel()
	f := start(t, nil, nil)
	conn, err := net.Dial("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	// Send a call and close at once: the reply's write fails.
	write(t, conn, []byte(`{"method":"io.systemd.UserDatabase.GetUserRecord","parameters":{"userName":"alice","service":"com.github.mnestor.ssoossh"}}`+"\x00"))
	_ = conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(f.logs.String(), "cannot answer a userdb peer") {
		if time.Now().After(deadline) {
			t.Fatalf("should log the failed write, got %s", f.logs)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestServeShouldReturnWhenTheListenerFails(t *testing.T) {
	t.Parallel()
	f := start(t, nil, nil)
	l, err := Listen(filepath.Join(filepath.Dir(f.socket), "other.sock"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if err := f.srv.Serve(t.Context(), l); err == nil {
		t.Fatal("should return the accept error when the listener is closed under it")
	}
}

// switchPeers is a PeerInspector whose credentials the test changes
// between connections; the accept loop reads them in accept order.
type switchPeers struct {
	mu   sync.Mutex
	cred Credentials
}

// set makes later connections report uid.
func (p *switchPeers) set(uid uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cred = Credentials{UID: uid, PID: 4242}
}

// Credentials implements PeerInspector.
func (p *switchPeers) Credentials(net.Conn) (Credentials, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cred, nil
}

// Cgroup implements PeerInspector: no peer is sshd.
func (p *switchPeers) Cgroup(int32) ([]byte, error) { return nil, os.ErrPermission }

// holdCalls starts n lookups of distinct names that wait on the blocked
// upstream, each on its own connection, and returns once all n reached
// upstream, so all n connections hold a slot. The returned wait group ends
// when they have been answered.
func holdCalls(t *testing.T, f *fixture, prefix string, n int) *sync.WaitGroup {
	t.Helper()
	before := f.nss.count()
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			c, err := varlink.Dial(context.Background(), f.socket)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = c.Close() }()
			_ = c.Call(context.Background(), userdb.MethodGetUserRecord, params("userName", fmt.Sprintf("%s%02d", prefix, i)), nil)
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.nss.count() < before+n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d held lookups reached upstream", f.nss.count()-before, n)
		}
		time.Sleep(time.Millisecond)
	}
	return &wg
}

// callRaw sends one parameterless call on a fresh raw connection and returns the error
// name of its reply ("" for success), how long the answer took, and
// whether the server then closed the connection within a second.
func callRaw(t *testing.T, socket, method string) (string, time.Duration, bool) {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	begin := time.Now()
	if err := varlink.NewEncoder(conn).WriteCall(varlink.Call{Method: method}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	dec := varlink.NewDecoder(conn)
	reply, err := dec.ReadReply()
	if err != nil {
		t.Fatalf("no reply: %v", err)
	}
	took := time.Since(begin)
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = dec.ReadReply()
	return reply.Error, took, errors.Is(err, io.EOF)
}

func TestServeShouldCapTheUserdbConnections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// held are the uids of the connections holding slots, in order.
		held []uint32
		// uid is the peer of the connection under test.
		uid       uint32
		wantError string
	}{
		{name: "should refuse an unprivileged peer when the unprivileged share is full", held: []uint32{1000, 1000, 1000}, uid: 1000, wantError: userdb.ErrorServiceNotAvailable},
		{name: "should serve a uid 0 peer when only the unprivileged share is full", held: []uint32{1000, 1000, 1000}, uid: 0},
		{name: "should refuse a uid 0 peer when every slot is taken", held: []uint32{1000, 1000, 1000, 0}, uid: 0, wantError: userdb.ErrorServiceNotAvailable},
		{name: "should serve an unprivileged peer when uid 0 holds the reserved slot", held: []uint32{0, 0}, uid: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			peers := &switchPeers{}
			// Four slots, one kept for uid 0.
			f := start(t, peers, func(c *config.Config) {
				c.SocketAnswerDeadline = 3 * time.Second
				c.LoginRefreshTimeout = time.Second
				c.SocketMaxConnections = 4
			})
			release := f.nss.blockCalls()
			var held []*sync.WaitGroup
			for i, uid := range tt.held {
				peers.set(uid)
				held = append(held, holdCalls(t, f, fmt.Sprintf("held%d-", i), 1))
			}

			peers.set(tt.uid)
			name, took, closed := callRaw(t, f.socket, userdb.MethodGetInfo)
			release()
			for _, wg := range held {
				wg.Wait()
			}
			if name != tt.wantError {
				t.Fatalf("got error %q, want %q", name, tt.wantError)
			}
			if tt.wantError != "" && (took > time.Second || !closed) {
				t.Fatalf("should refuse at once and close: took %v, closed %v", took, closed)
			}
		})
	}
}

func TestServeShouldFreeASlotWhenAConnectionCloses(t *testing.T) {
	t.Parallel()
	f := start(t, fakePeers{cred: Credentials{UID: 1000}}, func(c *config.Config) {
		c.SocketAnswerDeadline = 3 * time.Second
		c.LoginRefreshTimeout = time.Second
		c.SocketMaxConnections = 4
	})
	release := f.nss.blockCalls()
	held := holdCalls(t, f, "held", 3)
	if name, _, _ := callRaw(t, f.socket, userdb.MethodGetInfo); name != userdb.ErrorServiceNotAvailable {
		t.Fatalf("should refuse while the cap is full, got %q", name)
	}
	release()
	held.Wait()

	// The held connections were answered and closed; their slots are free.
	deadline := time.Now().Add(5 * time.Second)
	for {
		name, _, _ := callRaw(t, f.socket, userdb.MethodGetInfo)
		if name == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("should serve once a slot is free, still got %q", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServeShouldLogOverCapRefusalsAtMostOncePerInterval(t *testing.T) {
	t.Parallel()
	f := start(t, fakePeers{cred: Credentials{UID: 1000}}, func(c *config.Config) {
		c.SocketAnswerDeadline = 3 * time.Second
		c.LoginRefreshTimeout = time.Second
		c.SocketMaxConnections = 4
	})
	release := f.nss.blockCalls()
	held := holdCalls(t, f, "held", 3)
	defer func() { release(); held.Wait() }()

	const msg = "userdb connections over socket_max_connections refused"
	for range 5 {
		callRaw(t, f.socket, userdb.MethodGetInfo)
	}
	if n := strings.Count(f.logs.String(), msg); n != 1 {
		t.Fatalf("should log the first of five refusals only, logged %d lines:\n%s", n, f.logs)
	}
	f.clock.Advance(overCapLogInterval)
	callRaw(t, f.socket, userdb.MethodGetInfo)
	logs := f.logs.String()
	if n := strings.Count(logs, msg); n != 2 {
		t.Fatalf("should log again once the interval passed, logged %d lines", n)
	}
	if !strings.Contains(logs, "refused=5") || !strings.Contains(logs, "last_uid=1000") {
		t.Fatalf("should count the refusals since the last line, got:\n%s", logs)
	}
}

func TestAdmitShouldCloseUnansweredWhenTooManyRefusalsAreRunning(t *testing.T) {
	t.Parallel()
	logs := &syncBuffer{}
	s := New(Options{Peers: fakePeers{cred: Credentials{UID: 1000}}, MaxConnections: 4, AnswerDeadline: time.Second,
		Log: slog.New(slog.NewTextHandler(logs, nil))})
	for range 3 {
		if !s.slots.acquire(1000) {
			t.Fatal("cannot fill the unprivileged share")
		}
	}
	for range maxRefusing {
		s.refusing <- struct{}{}
	}
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	if run := s.admit(t.Context(), server); run != nil {
		t.Fatal("should not start a refusal when maxRefusing are running")
	}
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("should close the connection at once, read gave %v", err)
	}
	if !strings.Contains(logs.String(), "unanswered=1") {
		t.Fatalf("should count the unanswered refusal, got %s", logs)
	}
}

func TestConnSlots(t *testing.T) {
	t.Parallel()

	// op is acquire (+uid) or release (-uid) of one slot.
	type op struct {
		release bool
		uid     uint32
		want    bool
	}
	tests := []struct {
		name  string
		limit int
		ops   []op
	}{
		{name: "should keep a quarter for uid 0 when unprivileged peers fill the rest", limit: 8, ops: []op{
			{uid: 1, want: true}, {uid: 1, want: true}, {uid: 2, want: true}, {uid: 3, want: true},
			{uid: 4, want: true}, {uid: 5, want: true}, {uid: 6, want: false}, {uid: 0, want: true}, {uid: 0, want: true}, {uid: 0, want: false},
		}},
		{name: "should let uid 0 take every slot when no one else holds one", limit: 4, ops: []op{
			{uid: 0, want: true}, {uid: 0, want: true}, {uid: 0, want: true}, {uid: 0, want: true}, {uid: 0, want: false},
		}},
		{name: "should refuse an unprivileged peer when uid 0 holds every slot left", limit: 4, ops: []op{
			{uid: 1, want: true}, {uid: 0, want: true}, {uid: 0, want: true}, {uid: 0, want: true}, {uid: 1, want: false},
		}},
		{name: "should free a slot when one is released", limit: 4, ops: []op{
			{uid: 1, want: true}, {uid: 1, want: true}, {uid: 1, want: true}, {uid: 1, want: false},
			{release: true, uid: 1}, {uid: 1, want: true},
		}},
		{name: "should give a released uid 0 slot to an unprivileged peer when the share allows", limit: 4, ops: []op{
			{uid: 0, want: true}, {uid: 0, want: true}, {uid: 0, want: true}, {uid: 0, want: true}, {uid: 1, want: false},
			{release: true, uid: 0}, {uid: 1, want: true},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newConnSlots(tt.limit)
			for i, o := range tt.ops {
				if o.release {
					c.release(o.uid)
					continue
				}
				if got := c.acquire(o.uid); got != o.want {
					t.Fatalf("op %d: acquire(%d) = %v, want %v", i, o.uid, got, o.want)
				}
			}
		})
	}
}

func TestServeShouldCloseAConnectionWhenItsIdleTimeoutIsSpent(t *testing.T) {
	t.Parallel()
	// A read timeout far longer than the gaps between calls, so only the
	// idle budget can end the connection: with the fixture's 100ms one, a
	// gap stretched past it on a loaded machine ended it first.
	f := startWithReadTimeout(t, nil, func(c *config.Config) { c.SocketIdleTimeout = 300 * time.Millisecond }, 2*time.Second)
	conn, err := net.Dial("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	enc, dec := varlink.NewEncoder(conn), varlink.NewDecoder(conn)

	// A call every 50ms, each well inside the read timeout, would keep the
	// connection open for ever without the idle budget.
	begin := time.Now()
	for time.Since(begin) < 5*time.Second {
		if err := enc.WriteCall(varlink.Call{Method: userdb.MethodGetInfo}); err != nil {
			break
		}
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := dec.ReadReply(); err != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Under 1.5s: the 2s read timeout cannot have closed it, so the budget
	// did, whether at the top of the loop or through a read deadline cut to
	// what was left of it.
	if took := time.Since(begin); took < 250*time.Millisecond || took > 1500*time.Millisecond {
		t.Fatalf("should close once 300ms were spent waiting for calls, closed after %v", took)
	}
}

func TestServeShouldNotCutACallInFlightWhenItOutlastsTheIdleTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(ctx context.Context, c *varlink.Client) error
	}{
		{name: "should answer a lookup when upstream is slower than the idle timeout", call: func(ctx context.Context, c *varlink.Client) error {
			var raw json.RawMessage
			if err := c.Call(ctx, userdb.MethodGetUserRecord, params("userName", "alice"), &raw); err != nil {
				return err
			}
			if !strings.Contains(string(raw), `"userName":"alice"`) {
				return fmt.Errorf("got %s", raw)
			}
			return nil
		}},
		{name: "should finish a GetMemberships stream when upstream is slower than the idle timeout", call: func(ctx context.Context, c *varlink.Client) error {
			n := 0
			if err := c.CallMore(ctx, userdb.MethodGetMemberships, params("userName", "alice"), func(json.RawMessage) error { n++; return nil }); err != nil {
				return err
			}
			if n != 1 {
				return fmt.Errorf("got %d memberships, want 1", n)
			}
			return nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := start(t, nil, func(c *config.Config) {
				c.SocketIdleTimeout = 200 * time.Millisecond
				c.SocketAnswerDeadline = 3 * time.Second
				c.LoginRefreshTimeout = time.Second
			})
			c := f.dial(t)
			release := f.nss.blockCalls()
			time.AfterFunc(600*time.Millisecond, release)
			if err := tt.call(t.Context(), c); err != nil {
				t.Fatalf("the call in flight was cut: %v", err)
			}
			// The call did not use up the idle budget: the next one on the
			// same connection is answered too.
			if err := c.Call(t.Context(), userdb.MethodGetInfo, nil, nil); err != nil {
				t.Fatalf("should answer the next call, got %v", err)
			}
		})
	}
}

// scriptedListener is a net.Listener whose Accept returns each step in
// turn: a connection or an error.
type scriptedListener struct {
	mu    sync.Mutex
	steps []any
}

// Accept implements net.Listener.
func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.steps) == 0 {
		return nil, net.ErrClosed
	}
	step := l.steps[0]
	l.steps = l.steps[1:]
	if err, ok := step.(error); ok {
		return nil, err
	}
	return step.(net.Conn), nil
}

// Close implements net.Listener.
func (l *scriptedListener) Close() error { return nil }

// Addr implements net.Listener.
func (l *scriptedListener) Addr() net.Addr { return &net.UnixAddr{Name: "scripted", Net: "unix"} }

func TestAcceptLoopShouldRetryWhenAcceptRunsOutOfDescriptors(t *testing.T) {
	t.Parallel()

	emfile := &net.OpError{Op: "accept", Net: "unix", Err: os.NewSyscallError("accept", syscall.EMFILE)}
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	l := &scriptedListener{steps: []any{emfile, emfile, server}}
	logs := &syncBuffer{}
	s := New(Options{AnswerDeadline: time.Second, Log: slog.New(slog.NewTextHandler(logs, nil))})

	var dispatched int
	err := s.acceptLoop(t.Context(), l, func(_ context.Context, conn net.Conn) func() {
		dispatched++
		_ = conn.Close()
		return nil
	})
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("should return when the listener closes, got %v", err)
	}
	if dispatched != 1 {
		t.Fatalf("should accept the connection after two EMFILE, dispatched %d", dispatched)
	}
	if n := strings.Count(logs.String(), "cannot accept a connection; retrying"); n != 2 {
		t.Fatalf("should log each retry, logged %d:\n%s", n, logs)
	}
}

// cancelOnRetry is a slog.Handler that cancels a context when the accept
// loop logs its retry for the after-th time, so shutdown begins between
// that log line and the backoff it announces.
type cancelOnRetry struct {
	mu     sync.Mutex
	after  int
	cancel context.CancelFunc
}

// Enabled implements slog.Handler.
func (h *cancelOnRetry) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements slog.Handler.
func (h *cancelOnRetry) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Message == "cannot accept a connection; retrying" {
		if h.after--; h.after == 0 {
			h.cancel()
		}
	}
	return nil
}

// WithAttrs implements slog.Handler.
func (h *cancelOnRetry) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup implements slog.Handler.
func (h *cancelOnRetry) WithGroup(string) slog.Handler { return h }

func TestAcceptLoopShouldStopWhenShutdownBeginsDuringABackoff(t *testing.T) {
	t.Parallel()

	// Shutdown begins after the accept error was weighed and logged, so
	// only the backoff can see it. The fourth retry waits 40ms, far longer
	// than the gap between the log line and the wait, so the wait cannot
	// end before it notices the shutdown.
	const retries = 4
	enfile := &net.OpError{Op: "accept", Net: "unix", Err: os.NewSyscallError("accept", syscall.ENFILE)}
	l := &scriptedListener{}
	for range retries + 1 {
		l.steps = append(l.steps, enfile)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := New(Options{AnswerDeadline: time.Second, Log: slog.New(&cancelOnRetry{after: retries, cancel: cancel})})
	if err := s.acceptLoop(ctx, l, func(context.Context, net.Conn) func() { return nil }); err != nil {
		t.Fatalf("should return nil when shutting down, got %v", err)
	}
	if left := len(l.steps); left != 1 {
		t.Fatalf("should stop in the backoff before accepting again, %d accept steps left, want 1", left)
	}
}

func TestSleepContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		cancel bool
		d      time.Duration
		want   bool
	}{
		{name: "should report the wait passed when the context lives", d: time.Millisecond, want: true},
		{name: "should report the wait cut short when the context ends", cancel: true, d: time.Hour, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			if got := sleepContext(ctx, tt.d); got != tt.want {
				t.Fatalf("sleepContext = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRefuseShouldCloseUnanswered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		send func(t *testing.T, conn net.Conn)
	}{
		{name: "should close unanswered when the peer sends garbage", send: func(t *testing.T, conn net.Conn) {
			if _, err := conn.Write([]byte("not a call\x00")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "should close unanswered when the peer sends a oneway call", send: func(t *testing.T, conn net.Conn) {
			if err := varlink.NewEncoder(conn).WriteCall(varlink.Call{Method: userdb.MethodGetUserRecord, Oneway: true}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := New(Options{AnswerDeadline: time.Second, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
			server, client := net.Pipe()
			defer func() { _ = client.Close() }()
			done := make(chan struct{})
			go func() { defer close(done); s.refuse(server) }()
			// Before the send: net.Pipe refuses a deadline once the other
			// end has closed.
			if err := client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			tt.send(t, client)
			if n, err := client.Read(make([]byte, 64)); !errors.Is(err, io.EOF) {
				t.Fatalf("should close without a reply, read %d bytes, %v", n, err)
			}
			<-done
		})
	}
}

func TestRefuseShouldWriteNothingWhenThePeerClosesWithoutACall(t *testing.T) {
	t.Parallel()
	logs := &syncBuffer{}
	s := New(Options{AnswerDeadline: time.Second, Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	server, client := net.Pipe()
	_ = client.Close()
	s.refuse(server)
	// A refusal written to the closed pipe would fail and be logged.
	if strings.Contains(logs.String(), "cannot refuse a userdb peer") {
		t.Fatalf("should not try to answer a peer that sent no call, got %s", logs)
	}
}

func TestRefuseShouldLogWhenThePeerLeavesBeforeTheRefusal(t *testing.T) {
	t.Parallel()
	logs := &syncBuffer{}
	s := New(Options{AnswerDeadline: time.Second, Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() { defer close(done); s.refuse(server) }()
	// net.Pipe is unbuffered: the write returns once refuse has read the
	// call, and nothing reads the refusal, so it fails whether it starts
	// before or after the close.
	if err := varlink.NewEncoder(client).WriteCall(varlink.Call{Method: userdb.MethodGetUserRecord}); err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	<-done
	if !strings.Contains(logs.String(), "cannot refuse a userdb peer") {
		t.Fatalf("should log the refusal it could not write, got %s", logs)
	}
}

func TestHandleShouldCloseAtTheNextCallWhenAWaitSpentTheIdleBudget(t *testing.T) {
	t.Parallel()
	f := start(t, nil, nil)
	const idle = 300 * time.Millisecond
	logs := &syncBuffer{}
	s := New(Options{Daemon: f.daemon, AnswerDeadline: time.Second, IdleTimeout: idle,
		Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	// Every wait reports the whole budget spent, though the call came at
	// once: the call read is still answered, and the connection is closed
	// before the next read.
	s.since = func(time.Time) time.Duration { return idle }

	server, client := net.Pipe()
	defer func() { _ = client.Close() }()
	done := make(chan struct{})
	go func() { defer close(done); s.handle(t.Context(), server, Credentials{UID: 1000, PID: 4242}) }()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := varlink.NewEncoder(client).WriteCall(varlink.Call{Method: userdb.MethodGetInfo}); err != nil {
		t.Fatal(err)
	}
	dec := varlink.NewDecoder(client)
	if r, err := dec.ReadReply(); err != nil || r.Error != "" {
		t.Fatalf("should answer the call read, got %+v, %v", r, err)
	}
	if _, err := dec.ReadReply(); !errors.Is(err, io.EOF) {
		t.Fatalf("should close the connection after the answer, got %v", err)
	}
	<-done
	if !strings.Contains(logs.String(), "closing a userdb connection that spent socket_idle_timeout waiting for calls") {
		t.Fatalf("should log why it closed, got %s", logs)
	}
}
