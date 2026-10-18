package server

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mnestor/ssoossh/nssd/userdb"
)

func TestAdmin(t *testing.T) {
	t.Parallel()
	f := start(t, nil, nil)
	ctx := t.Context()
	if err := f.dial(t).Call(ctx, userdb.MethodGetUserRecord, params("userName", "alice"), nil); err != nil {
		t.Fatal(err)
	}

	resp, err := CallAdmin(ctx, f.admin, AdminRequest{Op: AdminStatus})
	if err != nil || resp.Status == nil || resp.Status.Cache.Users != 1 || resp.Status.Upstream.KeyID != "nss_k" {
		t.Fatalf("got %+v, %v", resp, err)
	}

	tests := []struct {
		name    string
		req     AdminRequest
		want    int
		wantErr string
	}{
		{name: "should mark one user when named", req: AdminRequest{Op: AdminInvalidate, Users: true, Name: "alice"}, want: 1},
		{name: "should mark everything when both kinds are given", req: AdminRequest{Op: AdminInvalidate, Users: true, Groups: true}, want: 2},
		{name: "should refuse invalidate when no kind is given", req: AdminRequest{Op: AdminInvalidate}, wantErr: "needs users"},
		{name: "should reload when asked", req: AdminRequest{Op: AdminReload}},
		{name: "should refuse an unknown operation when one is sent", req: AdminRequest{Op: "explode"}, wantErr: "unknown admin operation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := CallAdmin(ctx, f.admin, tt.req)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil || resp.Marked != tt.want {
				t.Fatalf("got %+v, %v", resp, err)
			}
		})
	}
	if !strings.Contains(f.logs.String(), "host files reloaded") {
		t.Fatalf("got logs %s", f.logs)
	}
	info, err := os.Stat(f.admin)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("should bind the admin socket 0600, got %v, %v", info, err)
	}
}

func TestAdminShouldRefuseAMalformedRequestWhenOneIsSent(t *testing.T) {
	t.Parallel()
	f := start(t, nil, nil)
	conn, err := net.Dial("unix", f.admin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	write(t, conn, []byte("{nope\n"))
	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "malformed admin request") {
		t.Fatalf("got %q", buf[:n])
	}
}

func TestAdminShouldRefuseAPeerWhenItIsNeitherRootNorTheDaemonsUser(t *testing.T) {
	t.Parallel()
	f := start(t, fakePeers{cred: Credentials{UID: 4321}}, nil)
	if _, err := CallAdmin(t.Context(), f.admin, AdminRequest{Op: AdminStatus}); err == nil || !strings.Contains(err.Error(), "without answering") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(f.logs.String(), "refusing an admin connection") {
		t.Fatalf("got logs %s", f.logs)
	}
}

func TestCallAdminShouldFailWhenNoDaemonListens(t *testing.T) {
	t.Parallel()
	_, err := CallAdmin(t.Context(), filepath.Join(socketDir(t), "absent.sock"), AdminRequest{Op: AdminStatus})
	if !errors.Is(err, ErrAdminUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestCallAdminShouldFailWhenTheRequestCannotBeSent(t *testing.T) {
	t.Parallel()
	dir := socketDir(t)
	path := filepath.Join(dir, "a.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		conn, err := l.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()
	// The listener closes at once: the write or the read fails, and either
	// way it is the same refusal.
	if _, err := CallAdmin(t.Context(), path, AdminRequest{Op: AdminStatus}); err == nil || !strings.Contains(err.Error(), "without answering") {
		t.Fatalf("got %v", err)
	}
}

// adminConn is a net.Conn whose writes fail with writeErr and whose reads
// return what is in read, for exchangeAdmin's outcomes without a race.
type adminConn struct {
	net.Conn
	writeErr error
	read     *strings.Reader
}

// Write fails with writeErr, or takes the bytes when it is nil.
func (c *adminConn) Write(b []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(b), nil
}

// Read reads the canned answer.
func (c *adminConn) Read(b []byte) (int, error) { return c.read.Read(b) }

func TestExchangeAdminShouldReportEachWayACallEnds(t *testing.T) {
	t.Parallel()

	// What a write to a socket the daemon already closed fails with.
	closedWrite := func(errno syscall.Errno) error {
		return &net.OpError{Op: "write", Net: "unix", Err: os.NewSyscallError("write", errno)}
	}
	tests := []struct {
		name     string
		writeErr error
		answer   string
		want     string
		wantResp AdminResponse
	}{
		{name: "should say the daemon closed without answering when the write fails with EPIPE", writeErr: closedWrite(syscall.EPIPE), want: "without answering"},
		{name: "should say the daemon closed without answering when the write fails with ECONNRESET", writeErr: closedWrite(syscall.ECONNRESET), want: "without answering"},
		{name: "should report the write error itself when it is anything else", writeErr: closedWrite(syscall.ENOSPC), want: "admin call: write unix: write: no space left on device"},
		{name: "should say the daemon closed without answering when it closes before the answer", answer: "", want: "without answering"},
		{name: "should return the daemon's error when it answers with one", answer: `{"error":"unknown admin operation \"x\""}` + "\n", want: `unknown admin operation "x"`, wantResp: AdminResponse{Error: `unknown admin operation "x"`}},
		{name: "should return the answer when the daemon gives one", answer: `{"marked":3}` + "\n", wantResp: AdminResponse{Marked: 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, err := exchangeAdmin(&adminConn{writeErr: tt.writeErr, read: strings.NewReader(tt.answer)}, AdminRequest{Op: AdminStatus})
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("got %v, want no error", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("got %v, want %q", err, tt.want)
			}
			if resp.Marked != tt.wantResp.Marked || resp.Error != tt.wantResp.Error {
				t.Fatalf("got %+v, want %+v", resp, tt.wantResp)
			}
		})
	}
}

func TestListenAdminShouldFailWhenTheDirectoryCannotBeMade(t *testing.T) {
	t.Parallel()
	file := filepath.Join(socketDir(t), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenAdmin(filepath.Join(file, "admin.sock")); err == nil {
		t.Fatal("want an error")
	}
}
