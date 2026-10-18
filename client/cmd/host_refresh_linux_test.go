//go:build linux

package cmd

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/varlink"
)

// fakeDaemon listens on a socket in a temporary directory and hands back
// the first call it reads, if any arrives within the test's patience.
func fakeDaemon(t *testing.T) (string, <-chan varlink.Call) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "com.github.mnestor.ssoossh")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	calls := make(chan varlink.Call, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		call, err := varlink.NewDecoder(conn).ReadCall()
		if err == nil {
			calls <- call
		}
	}()
	return path, calls
}

// should send RefreshUser one-way, with the user and nothing else, when
// the daemon is listening.
func TestNSSRefreshShouldSendRefreshUserOnewayWhenTheDaemonListens(t *testing.T) {
	t.Parallel()

	path, calls := fakeDaemon(t)
	nssRefresh{socket: path, timeout: time.Second}.send(context.Background(), "alice")

	var call varlink.Call
	select {
	case call = <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon received no call")
	}
	var params map[string]any
	if err := json.Unmarshal(call.Parameters, &params); err != nil {
		t.Fatalf("decode parameters %s: %v", call.Parameters, err)
	}
	if call.Method != nssRefreshMethod || !call.Oneway || call.More || len(params) != 1 || params["userName"] != "alice" {
		t.Errorf("got method %q oneway %v more %v parameters %s, want %s one-way with userName alice",
			call.Method, call.Oneway, call.More, call.Parameters, nssRefreshMethod)
	}
}

// should send nothing when there is no user to refresh.
func TestNSSRefreshShouldSendNothingWhenTheUserIsEmpty(t *testing.T) {
	t.Parallel()

	path, calls := fakeDaemon(t)
	nssRefresh{socket: path, timeout: time.Second}.send(context.Background(), "")

	select {
	case call := <-calls:
		t.Errorf("sent %q for an empty user", call.Method)
	case <-time.After(200 * time.Millisecond):
	}
}

// should return quietly and within its timeout whatever stands in the
// daemon's place.
func TestNSSRefreshShouldReturnQuietlyWhenTheDaemonIsUnreachable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T) string
	}{
		{name: "should return when the socket does not exist", setup: func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "absent")
		}},
		{name: "should return when the directory does not exist", setup: func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "no", "such", "dir", "sock")
		}},
		{name: "should return when the path is a regular file", setup: func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{name: "should return when nothing listens on the socket", setup: func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "stale")
			ln, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			ln.(*net.UnixListener).SetUnlinkOnClose(false)
			_ = ln.Close()
			return path
		}},
		{name: "should return when the daemon accepts and never reads", setup: func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "silent")
			ln, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ln.Close() })
			return path
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := tt.setup(t)
			start := time.Now()
			nssRefresh{socket: path, timeout: 50 * time.Millisecond}.send(context.Background(), "alice")
			if took := time.Since(start); took > 2*time.Second {
				t.Errorf("send took %v with a 50ms timeout", took)
			}
		})
	}
}

// should use the daemon's socket and a 50 ms bound by default.
func TestNewNSSRefreshShouldTargetTheInstalledDaemonWhenBuiltByDefault(t *testing.T) {
	t.Parallel()

	if got := newNSSRefresh(); got.socket != "/run/systemd/userdb/com.github.mnestor.ssoossh" || got.timeout != 50*time.Millisecond {
		t.Errorf("newNSSRefresh() = %+v", got)
	}
}
