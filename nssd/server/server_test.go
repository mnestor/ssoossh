package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	if err := c.Call(ctx, userdb.MethodGetInfo, nil, &info); err != nil || info["product"] != "ssoosshd-nss" {
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

	tests := []struct {
		name string
		send func(t *testing.T, conn net.Conn)
	}{
		{name: "should close when no call arrives within the read timeout", send: func(*testing.T, net.Conn) {}},
		{name: "should close when the call is not JSON", send: func(t *testing.T, conn net.Conn) { write(t, conn, []byte("not json\x00")) }},
		{name: "should close when a message passes 64 KiB", send: func(t *testing.T, conn net.Conn) {
			write(t, conn, bytes.Repeat([]byte("a"), varlink.MaxMessageSize+2))
		}},
		{name: "should close when a call has no method", send: func(t *testing.T, conn net.Conn) { write(t, conn, []byte(`{"parameters":{}}`+"\x00")) }},
		{name: "should close after a call when the connection then sits idle", send: func(t *testing.T, conn net.Conn) {
			write(t, conn, []byte(`{"method":"org.varlink.service.GetInfo"}`+"\x00"))
			if _, err := varlink.NewDecoder(conn).ReadReply(); err != nil {
				t.Errorf("first reply: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := start(t, nil, nil)
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
	if elapsed > 300*time.Millisecond || elapsed < 150*time.Millisecond {
		t.Fatalf("should answer within socket_answer_deadline (300ms), took %v", elapsed)
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
