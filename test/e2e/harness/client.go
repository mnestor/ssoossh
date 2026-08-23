//go:build e2e || resilience || load

package harness

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"testing"
	"time"
)

var approvalURLPattern = regexp.MustCompile(`https?://\S+`)

// LoginProcess is a running `ssoossh ssh login` subprocess. It runs from
// ssh_config's Match exec in production and, correspondingly, prints
// everything a human reads to stderr and blocks on the server's SSE stream
// until approved, denied, or the request expires — so the harness has to
// observe the approval URL while the process is still running, not after.
type LoginProcess struct {
	cmd    *exec.Cmd
	stdout lockedBuffer

	mu          sync.Mutex
	stderr      bytes.Buffer
	approvalURL string
	urlSeen     chan struct{}
	urlOnce     sync.Once

	done    chan struct{}
	waitErr error
}

// StartLogin runs `ssoossh ssh login` against serverURL with agentSocket as
// its SSH_AUTH_SOCK, in a fresh working directory so no stray
// ./ssoossh.yaml in the repo can influence it. extraArgs are appended after
// the fixed flags (e.g. "--force").
func StartLogin(t *testing.T, ssoosshPath, serverURL, agentSocket string, extraArgs ...string) *LoginProcess {
	t.Helper()

	args := append([]string{"ssh", "login", "--server", serverURL}, extraArgs...)
	cmd := exec.Command(ssoosshPath, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+agentSocket)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("harness: failed to attach stderr pipe: %v", err)
	}

	lp := &LoginProcess{
		urlSeen: make(chan struct{}),
		done:    make(chan struct{}),
	}
	cmd.Stdout = &lp.stdout
	lp.cmd = cmd

	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: failed to start ssoossh ssh login: %v", err)
	}

	go lp.scanStderr(stderrPipe)
	go func() {
		lp.waitErr = cmd.Wait()
		close(lp.done)
	}()

	return lp
}

// scanStderr copies r line by line into lp.stderr and, the first time a URL
// appears in a line, publishes it via urlSeen — the process is typically
// still blocked on SSE at that point, which is exactly the property the
// "approval URL printed before completion" assertion needs to observe.
func (lp *LoginProcess) scanStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()

		lp.mu.Lock()
		lp.stderr.WriteString(line)
		lp.stderr.WriteByte('\n')
		lp.mu.Unlock()

		if m := approvalURLPattern.FindString(line); m != "" {
			lp.setApprovalURL(m)
		}
	}
}

func (lp *LoginProcess) setApprovalURL(url string) {
	lp.urlOnce.Do(func() {
		lp.mu.Lock()
		lp.approvalURL = url
		lp.mu.Unlock()
		close(lp.urlSeen)
	})
}

// ApprovalURL blocks until the process has printed its approval URL (per
// ssh_login.go, always before it starts waiting on SSE), the process exits
// first, or timeout passes.
func (lp *LoginProcess) ApprovalURL(t *testing.T, timeout time.Duration) string {
	t.Helper()

	select {
	case <-lp.urlSeen:
		lp.mu.Lock()
		defer lp.mu.Unlock()
		return lp.approvalURL
	case <-lp.done:
		t.Fatalf("harness: ssoossh ssh login exited before printing an approval URL (%v)\nstderr:\n%s",
			lp.waitErr, lp.Stderr())
	case <-time.After(timeout):
		t.Fatalf("harness: timed out waiting for ssoossh ssh login to print an approval URL\nstderr so far:\n%s",
			lp.Stderr())
	}
	return ""
}

// Done returns a channel closed once the process has exited, for callers
// that want a non-blocking check (e.g. "assert it hasn't exited yet")
// rather than Wait's blocking one.
func (lp *LoginProcess) Done() <-chan struct{} { return lp.done }

// Wait blocks until the process exits, returning its error (nil on a clean
// exit). Fails the test if it doesn't exit before timeout.
func (lp *LoginProcess) Wait(t *testing.T, timeout time.Duration) error {
	t.Helper()

	select {
	case <-lp.done:
		return lp.waitErr
	case <-time.After(timeout):
		t.Fatalf("harness: ssoossh ssh login did not exit before timeout\nstderr so far:\n%s", lp.Stderr())
		return nil
	}
}

// Stdout returns everything the process has written to stdout so far.
// Production code sends nothing here — see ssh_login.go's doc comment — so
// this is mainly a sanity check that it stayed that way.
func (lp *LoginProcess) Stdout() string { return lp.stdout.String() }

// Stderr returns everything the process has written to stderr so far.
func (lp *LoginProcess) Stderr() string {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	return lp.stderr.String()
}

// RunLogout runs `ssoossh ssh logout` to completion and returns its
// combined output plus any error.
func RunLogout(t *testing.T, ssoosshPath, serverURL, agentSocket string) (output string, err error) {
	t.Helper()

	cmd := exec.Command(ssoosshPath, "ssh", "logout", "--server", serverURL)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+agentSocket)

	out, runErr := cmd.CombinedOutput()
	return string(out), runErr
}
