package server

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// listenerFile returns the file of a fresh listening socket, standing in
// for the descriptor systemd would pass.
func listenerFile(t *testing.T) (*os.File, string) {
	t.Helper()
	path := filepath.Join(socketDir(t), "act.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	f, err := l.(*net.UnixListener).File()
	if err != nil {
		t.Fatal(err)
	}
	return f, path
}

func TestActivationListener(t *testing.T) {
	t.Parallel()
	const pid = 4242
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}

	tests := []struct {
		name    string
		vars    map[string]string
		file    func(t *testing.T) func(uintptr, string) *os.File
		wantErr string
	}{
		{name: "should report not activated when LISTEN_FDS is unset", vars: map[string]string{}, wantErr: "not socket-activated"},
		{name: "should report not activated when LISTEN_PID names another process", vars: map[string]string{"LISTEN_PID": "1", "LISTEN_FDS": "1"}, wantErr: "not socket-activated"},
		{name: "should refuse two sockets when the unit must have one", vars: map[string]string{"LISTEN_PID": strconv.Itoa(pid), "LISTEN_FDS": "2"}, wantErr: "exactly one"},
		{name: "should refuse a garbled count when LISTEN_FDS is not a number", vars: map[string]string{"LISTEN_PID": strconv.Itoa(pid), "LISTEN_FDS": "x"}, wantErr: "exactly one"},
		{name: "should refuse a closed descriptor when nothing is open there", vars: map[string]string{"LISTEN_PID": strconv.Itoa(pid), "LISTEN_FDS": "1"},
			file: func(*testing.T) func(uintptr, string) *os.File { return func(uintptr, string) *os.File { return nil } }, wantErr: "not open"},
		{name: "should refuse a descriptor that is not a socket when it is a file", vars: map[string]string{"LISTEN_PID": strconv.Itoa(pid), "LISTEN_FDS": "1"},
			file: func(t *testing.T) func(uintptr, string) *os.File {
				f, err := os.CreateTemp(t.TempDir(), "x")
				if err != nil {
					t.Fatal(err)
				}
				return func(uintptr, string) *os.File { return f }
			}, wantErr: "not a listening socket"},
		{name: "should take the passed socket when activated", vars: map[string]string{"LISTEN_PID": strconv.Itoa(pid), "LISTEN_FDS": "1"},
			file: func(t *testing.T) func(uintptr, string) *os.File {
				f, _ := listenerFile(t)
				return func(fd uintptr, _ string) *os.File {
					if fd != listenFDsStart {
						t.Errorf("asked for fd %d", fd)
					}
					return f
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := Activation{Getenv: env(tt.vars), PID: pid}
			if tt.file != nil {
				a.File = tt.file(t)
			}
			l, err := a.Listener()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_ = l.Close()
		})
	}
}

func TestActivationShouldReadTheRealEnvironmentWhenGivenNothing(t *testing.T) {
	t.Parallel()
	// The test process is not socket-activated.
	if _, err := (Activation{}).Listener(); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("got %v", err)
	}
}

func TestListen(t *testing.T) {
	t.Parallel()
	dir := socketDir(t)

	path := filepath.Join(dir, "sub", "userdb.sock")
	l, err := Listen(path, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o666 {
		t.Fatalf("should bind 0666, got %v, %v", info, err)
	}
	// A stale socket left behind is replaced.
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
	l2, err := Listen(path, 0o666)
	if err != nil {
		t.Fatalf("should replace a stale socket, got %v", err)
	}
	_ = l2.Close()

	file := filepath.Join(dir, "regular")
	if err := os.WriteFile(file, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(file, 0o666); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("should refuse to replace a regular file, got %v", err)
	}
	if _, err := Listen(filepath.Join(file, "x.sock"), 0o666); err == nil {
		t.Fatal("should fail when the directory cannot be made")
	}
	long := filepath.Join(dir, strings.Repeat("d", 120)+".sock")
	if _, err := Listen(long, 0o666); err == nil {
		t.Fatal("should fail when the path is too long to bind")
	}
}
