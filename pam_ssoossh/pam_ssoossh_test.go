//go:build pam

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// recordingLogger keeps every line by level so tests can assert on what the
// entry point logs, not just on the code it returns.
type recordingLogger struct {
	debugs, infos, warnings, errors []string
	debug                           string
}

func (r *recordingLogger) Debugf(format string, v ...any) {
	r.debugs = append(r.debugs, fmt.Sprintf(format, v...))
}
func (r *recordingLogger) Infof(format string, v ...any) {
	r.infos = append(r.infos, fmt.Sprintf(format, v...))
}
func (r *recordingLogger) Noticef(format string, v ...any) {}
func (r *recordingLogger) Warningf(format string, v ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, v...))
}
func (r *recordingLogger) Errorf(format string, v ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, v...))
}
func (r *recordingLogger) SetDebug(d string) { r.debug = d }
func (r *recordingLogger) Close() error      { return nil }

// containsLine reports whether any line in lines contains want.
func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// newHangingPAMTestServer accepts the certificate request and then never
// resolves it, standing in for a human who never opens the browser.
func newHangingPAMTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/pam":
			writeEnvelope(w, map[string]string{
				"request_id":   "req-pam",
				"events_url":   "/api/certs/requests/req-pam/events",
				"approval_url": "/approve/req-pam",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-pam/events":
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestRun(t *testing.T) {
	t.Run("should log the module version at info before anything else", func(t *testing.T) {
		log := &recordingLogger{}
		run(context.Background(), log, &fakeConversation{}, "alice", nil)
		if len(log.infos) == 0 || !strings.HasPrefix(log.infos[0], "pam_ssoossh ") {
			t.Errorf("infos = %q, want the first line to be the version stamp", log.infos)
		}
	})

	t.Run("should apply the debug module argument to the logger", func(t *testing.T) {
		log := &recordingLogger{}
		run(context.Background(), log, &fakeConversation{}, "alice", []string{"debug"})
		if log.debug != "true" {
			t.Errorf("SetDebug got %q, want %q", log.debug, "true")
		}
	})

	t.Run("should leave debug unset when the argument is absent", func(t *testing.T) {
		log := &recordingLogger{debug: "stale"}
		run(context.Background(), log, &fakeConversation{}, "alice", nil)
		if log.debug != "" {
			t.Errorf("SetDebug got %q, want empty", log.debug)
		}
	})

	t.Run("should return the failure code and log the error when misconfigured", func(t *testing.T) {
		log := &recordingLogger{}
		code := run(context.Background(), log, &fakeConversation{}, "alice", nil)
		if code != PamUserUnknown {
			t.Errorf("code = %d, want PamUserUnknown", code)
		}
		if !containsLine(log.errors, "not configured correctly") {
			t.Errorf("errors = %q, want the configuration error logged", log.errors)
		}
	})

	t.Run("should not log a successful authentication on failure", func(t *testing.T) {
		log := &recordingLogger{}
		run(context.Background(), log, &fakeConversation{}, "alice", nil)
		if containsLine(log.infos, "successful authentication") {
			t.Errorf("infos = %q, want no success line on a failed attempt", log.infos)
		}
	})

	t.Run("should return PamSuccess and log it when the server issues a valid certificate", func(t *testing.T) {
		ca := newTestCA(t)
		caFile := writeAuthorizedKeysFile(t, ca.publicKey())
		ts := newPAMTestServerResolving(t, func(pub ssh.PublicKey, username string) (string, string) {
			now := time.Now()
			cert := ca.sign(t, pub, []string{username}, now.Add(-time.Second), now.Add(time.Minute))
			return "approved", string(ssh.MarshalAuthorizedKey(cert))
		})
		log := &recordingLogger{}
		args := []string{"server=" + ts.URL, "trusted-ca-file=" + caFile}

		code := run(context.Background(), log, &fakeConversation{}, "alice", args)
		if code != PamSuccess {
			t.Fatalf("code = %d, want PamSuccess (errors: %q)", code, log.errors)
		}
		if !containsLine(log.infos, "successful authentication: alice") {
			t.Errorf("infos = %q, want the success line", log.infos)
		}
		if len(log.errors) != 0 {
			t.Errorf("errors = %q, want none on success", log.errors)
		}
	})

	t.Run("should bound the attempt by the timeout module argument", func(t *testing.T) {
		caFile := writeAuthorizedKeysFile(t, newTestCA(t).publicKey())
		ts := newHangingPAMTestServer(t)
		log := &recordingLogger{}
		args := []string{"server=" + ts.URL, "trusted-ca-file=" + caFile, "timeout=100ms"}

		start := time.Now()
		code := run(context.Background(), log, &fakeConversation{}, "alice", args)
		if code != PamAuthErr {
			t.Errorf("code = %d, want PamAuthErr", code)
		}
		if !containsLine(log.errors, "timed out waiting for approval") {
			t.Errorf("errors = %q, want the timeout logged", log.errors)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("run took %v, want it bounded by the 100ms timeout argument", elapsed)
		}
	})

	t.Run("should abandon the attempt when the caller's context is cancelled", func(t *testing.T) {
		caFile := writeAuthorizedKeysFile(t, newTestCA(t).publicKey())
		ts := newHangingPAMTestServer(t)
		log := &recordingLogger{}
		args := []string{"server=" + ts.URL, "trusted-ca-file=" + caFile, "timeout=30s"}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		code := run(ctx, log, &fakeConversation{}, "alice", args)
		if code != PamAuthErr {
			t.Errorf("code = %d, want PamAuthErr", code)
		}
		if !containsLine(log.errors, "authentication was interrupted") {
			t.Errorf("errors = %q, want the interruption logged", log.errors)
		}
	})
}
