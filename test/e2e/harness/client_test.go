//go:build e2e || resilience || load

package harness

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeClient writes a shell script standing in for the ssoossh binary, so
// StartClient's process handling can be checked without a server.
func fakeClient(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ssoossh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // a stand-in binary has to be executable
		t.Fatal(err)
	}
	return path
}

// A client the test left running must be gone by the time the test's
// cleanup reaches the server it started first, or its open events request
// holds ssoosshd's shutdown to its five-second bound (#116).
func TestStartClient_ShouldKillAClientStillRunningAtCleanup(t *testing.T) {
	bin := fakeClient(t, "exec sleep 60")

	var cp *ClientProcess
	t.Run("leaves the client running", func(t *testing.T) {
		cp = StartClient(t, bin, ClientOptions{})
	})

	select {
	case <-cp.Done():
	default:
		t.Fatal("the client was still running after its test's cleanup")
	}
}

// Cleanup must not touch a client the test already waited for: its exit
// status is the test's evidence, and a kill would overwrite it.
func TestStartClient_ShouldLeaveAnExitedClientAlone(t *testing.T) {
	bin := fakeClient(t, "exit 0")

	var cp *ClientProcess
	t.Run("waits for the client", func(t *testing.T) {
		cp = StartClient(t, bin, ClientOptions{})
		if err := cp.Wait(t, 10*time.Second); err != nil {
			t.Fatalf("fake client failed: %v", err)
		}
	})

	if cp.waitErr != nil {
		t.Errorf("got exit error %v after cleanup, want the clean exit the test saw", cp.waitErr)
	}
}
