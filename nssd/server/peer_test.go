package server

import (
	"bytes"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestProcInspectorShouldReadThisProcessWhenItIsThePeer(t *testing.T) {
	t.Parallel()
	path := filepath.Join(socketDir(t), "peer.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		conn, err := net.Dial("unix", path)
		if err == nil {
			defer func() { _ = conn.Close() }()
			buf := make([]byte, 1)
			_, _ = conn.Read(buf)
		}
	}()
	conn, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	var p ProcInspector
	cred, err := p.Credentials(conn)
	if err != nil {
		t.Fatal(err)
	}
	if int(cred.UID) != os.Getuid() || int(cred.PID) != os.Getpid() {
		t.Fatalf("got %+v, want uid %d pid %d", cred, os.Getuid(), os.Getpid())
	}
	cgroup, err := p.Cgroup(cred.PID)
	if err != nil || len(cgroup) == 0 {
		t.Fatalf("should read this process's cgroup, got %q, %v", cgroup, err)
	}
}

func TestProcInspectorShouldFail(t *testing.T) {
	t.Parallel()
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	if _, err := (ProcInspector{}).Credentials(a); err == nil {
		t.Fatal("should refuse a connection that is not a socket")
	}

	// A descriptor that is not a socket fails SO_PEERCRED itself.
	file, err := os.CreateTemp(t.TempDir(), "notasocket")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := (ProcInspector{}).Credentials(fileConn{file: file}); err == nil {
		t.Fatal("should fail SO_PEERCRED on a regular file")
	}
	if _, err := (ProcInspector{Proc: t.TempDir()}).Cgroup(1); err == nil {
		t.Fatal("should fail for a pid with no cgroup file")
	}
}

func TestSSHDCheck(t *testing.T) {
	t.Parallel()
	units := []string{"sshd.service", "ssh.service", "sshd@.service", "ssh@.service"}

	tests := []struct {
		name   string
		cred   Credentials
		cgroup string
		want   bool
	}{
		{name: "should match sshd.service when the peer is root in it", cgroup: "0::/system.slice/sshd.service\n", want: true},
		{name: "should match ssh.service when Debian names the unit so", cgroup: "0::/system.slice/ssh.service\n", want: true},
		{name: "should match a template instance when sshd is socket-activated", cgroup: "0::/system.slice/system-sshd.slice/sshd@0-10.0.0.1:22-10.0.0.2:5555.service\n", want: true},
		{name: "should match the name=systemd line when the host is hybrid", cgroup: "12:pids:/system.slice/sshd.service\n1:name=systemd:/system.slice/sshd.service\n0::/\n", want: true},
		{name: "should not match a controller line when only it names sshd", cgroup: "12:pids:/system.slice/sshd.service\n0::/user.slice\n"},
		{name: "should not match a session scope when the user has logged in", cgroup: "0::/user.slice/user-0.slice/session-4.scope\n"},
		{name: "should not match a look-alike unit when the name only starts the same", cgroup: "0::/system.slice/sshd-keygen.service\n"},
		{name: "should not match a bare template when it has no instance", cgroup: "0::/system.slice/sshd@.service\n"},
		{name: "should not match a non-root peer when it runs in sshd.service", cred: Credentials{UID: 1000}, cgroup: "0::/system.slice/sshd.service\n"},
		{name: "should not match when the cgroup is unreadable", cgroup: ""},
		{name: "should not match when the file is garbage", cgroup: "garbage\nno:path\n0::relative\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			c := sshdCheck{units: units, peers: fakePeers{cgroup: tt.cgroup}, log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
			if got := c.isSSHD(tt.cred); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// fileConn is a net.Conn whose descriptor is a regular file.
type fileConn struct {
	net.Conn
	file *os.File
}

// SyscallConn exposes the file's descriptor.
func (c fileConn) SyscallConn() (syscall.RawConn, error) { return c.file.SyscallConn() }
