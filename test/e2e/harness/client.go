//go:build e2e || resilience || load

package harness

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"testing"
	"time"
)

var approvalURLPattern = regexp.MustCompile(`https?://\S+`)

// ClientOptions describes one invocation of the ssoossh client binary.
//
// The zero value is a usable invocation with no arguments: a fresh working
// directory, an isolated HOME, and no configuration file anywhere. Every
// field below adds exactly one thing to that, so a test states the
// environment it needs and nothing else.
type ClientOptions struct {
	// Args are passed to the binary as-is, e.g. {"ssh", "login",
	// "--server", srv.BaseURL}. No flags are added implicitly: a test that
	// wants the server on the command line says so, and a test exercising
	// configuration files deliberately does not.
	Args []string

	// Dir is the working directory. Empty means a fresh t.TempDir(), which
	// is what keeps a stray ./ssoossh.yaml in the repo from reaching the
	// client.
	Dir string

	// LocalYAML, if non-empty, is written to <Dir>/ssoossh.yaml — the
	// lowest-precedence location the client reads from the working
	// directory.
	LocalYAML string

	// UserYAML, if non-empty, is written to <HOME>/.config/ssoossh.yaml,
	// the per-user configuration file. It outranks the local file.
	UserYAML string

	// ConfigYAML, if non-empty, is written to a file outside the working
	// directory and named on the command line with --config, which outranks
	// every search-path location. Args must not also carry a --config.
	ConfigYAML string

	// Env adds to (or overrides) the inherited environment. A key mapped to
	// the empty string is still exported as empty — that is how
	// SSH_AUTH_SOCK is neutralized for the no-agent cases — so use
	// Unset for a variable that must not be present at all.
	Env map[string]string

	// Unset removes variables from the inherited environment entirely,
	// which is different from setting them empty: os.UserHomeDir and
	// ssh-agent discovery both distinguish the two.
	Unset []string
}

// ClientResult is a finished client invocation.
type ClientResult struct {
	// Stdout and Stderr are captured separately. The client prints
	// everything a person reads to stderr (see ssh_login.go), so a test
	// asserting that a command produced machine-readable output has to
	// check the right one.
	Stdout string
	Stderr string

	// ExitCode is the process's exit status: 0 on success, non-zero on any
	// failure the client reports. A CLI's exit status is half of its
	// contract, so a failure-path test should assert this and not only the
	// message.
	ExitCode int

	// Err is the error from waiting on the process. Non-nil whenever
	// ExitCode is non-zero; also non-nil if the process could not be run at
	// all, in which case ExitCode is -1.
	Err error
}

// Combined returns stdout and stderr concatenated, for failure messages
// that want everything the process said without caring which stream it
// used.
func (r ClientResult) Combined() string { return r.Stdout + r.Stderr }

// RunClient runs the client to completion and returns what it said and how
// it exited. It does not fail the test on a non-zero exit: a large share of
// the client's behavior is what it does when the user gets something wrong,
// and those tests need the failure, not a t.Fatal.
func RunClient(t *testing.T, ssoosshPath string, o ClientOptions) ClientResult {
	t.Helper()

	cmd, stdout, stderr := buildClientCommand(t, ssoosshPath, o)

	err := cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}

	return ClientResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: code,
		Err:      err,
	}
}

// buildClientCommand assembles the *exec.Cmd for o, materializing whatever
// configuration files it asks for. Shared by RunClient and StartClient so
// the two agree on the environment a client invocation gets.
func buildClientCommand(t *testing.T, ssoosshPath string, o ClientOptions) (*exec.Cmd, *lockedBuffer, *lockedBuffer) {
	t.Helper()

	dir := o.Dir
	if dir == "" {
		dir = t.TempDir()
	}

	// HOME is isolated on every invocation, not only when UserYAML is set.
	// Without this the client reads the developer's own
	// ~/.config/ssoossh.yaml, so whether the suite passes depends on who is
	// running it — the same hazard the fresh working directory already
	// guards against for ./ssoossh.yaml.
	home := t.TempDir()
	if o.UserYAML != "" {
		writeConfigFile(t, filepath.Join(home, ".config", "ssoossh.yaml"), o.UserYAML)
	}
	if o.LocalYAML != "" {
		writeConfigFile(t, filepath.Join(dir, "ssoossh.yaml"), o.LocalYAML)
	}

	args := append([]string{}, o.Args...)
	if o.ConfigYAML != "" {
		// Outside dir and outside home, so this file is reachable only
		// through the --config flag and cannot be picked up as one of the
		// search-path locations by accident.
		named := filepath.Join(t.TempDir(), "named.yaml")
		writeConfigFile(t, named, o.ConfigYAML)
		args = append(args, "--config", named)
	}

	cmd := exec.Command(ssoosshPath, args...)
	cmd.Dir = dir
	cmd.Env = clientEnv(home, o)

	var stdout, stderr lockedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	return cmd, &stdout, &stderr
}

// clientEnv builds the child environment: the parent's, with HOME pointed
// at the isolated directory, then o.Unset removed, then o.Env applied last
// so a test can override anything above it.
func clientEnv(home string, o ClientOptions) []string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := splitEnv(kv); ok {
			env[k] = v
		}
	}

	env["HOME"] = home

	for _, k := range o.Unset {
		delete(env, k)
	}
	for k, v := range o.Env {
		env[k] = v
	}

	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	// Sorted so a failure message quoting the environment is stable and
	// diffable between runs.
	sort.Strings(out)
	return out
}

// splitEnv splits a "KEY=VALUE" entry. Entries without '=' are not
// environment variables and are dropped.
func splitEnv(kv string) (key, value string, ok bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return "", "", false
}

// writeConfigFile writes content to path, creating parent directories. 0600
// on the file rather than 0644: these stand in for a user's own
// configuration, and one of the things the suite will assert is that the
// client is content with a file only its owner can read.
func writeConfigFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("harness: failed to create config directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("harness: failed to write config file %s: %v", path, err)
	}
}

// ClientProcess is a running ssoossh client subprocess.
//
// The streaming form exists because two commands block: `ssh login` and
// `service enroll` both print an approval URL and then wait on the server's
// SSE stream until approved, denied, or expired. A test has to observe the
// URL while the process is still running, not after it exits.
type ClientProcess struct {
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

// StartClient starts the client and returns without waiting for it. Use
// RunClient for a command that finishes on its own.
func StartClient(t *testing.T, ssoosshPath string, o ClientOptions) *ClientProcess {
	t.Helper()

	cmd, _, _ := buildClientCommand(t, ssoosshPath, o)

	cp := &ClientProcess{
		urlSeen: make(chan struct{}),
		done:    make(chan struct{}),
	}

	// The buffers buildClientCommand attached are replaced: stdout goes to
	// this process's own buffer, and stderr needs a pipe rather than a
	// buffer so scanStderr can publish the approval URL as it appears
	// instead of after the process exits.
	cmd.Stdout = &cp.stdout
	cmd.Stderr = nil
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("harness: failed to attach stderr pipe: %v", err)
	}
	cp.cmd = cmd

	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: failed to start %s %v: %v", ssoosshPath, o.Args, err)
	}

	go cp.scanStderr(stderrPipe)
	go func() {
		cp.waitErr = cmd.Wait()
		close(cp.done)
	}()

	return cp
}

// StartLogin runs `ssoossh ssh login` against serverURL with agentSocket as
// its SSH_AUTH_SOCK, in a fresh working directory so no stray
// ./ssoossh.yaml in the repo can influence it. extraArgs are appended after
// the fixed flags (e.g. "--force").
//
// A thin wrapper over StartClient, kept because it is what most of the
// suite wants and because the three suites that share this harness have
// dozens of call sites. Reach for StartClient directly when the invocation
// needs configuration files, a different command, or a modified
// environment.
func StartLogin(t *testing.T, ssoosshPath, serverURL, agentSocket string, extraArgs ...string) *ClientProcess {
	t.Helper()

	return StartClient(t, ssoosshPath, ClientOptions{
		Args: append([]string{"ssh", "login", "--server", serverURL}, extraArgs...),
		// Exported even when empty, which is what the callers passing ""
		// rely on: an empty SSH_AUTH_SOCK is an agent that cannot be
		// dialed, not an absent variable.
		Env: map[string]string{"SSH_AUTH_SOCK": agentSocket},
	})
}

// scanStderr copies r line by line into cp.stderr and, the first time a URL
// appears in a line, publishes it via urlSeen — the process is typically
// still blocked on SSE at that point, which is exactly the property the
// "approval URL printed before completion" assertion needs to observe.
func (cp *ClientProcess) scanStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()

		cp.mu.Lock()
		cp.stderr.WriteString(line)
		cp.stderr.WriteByte('\n')
		cp.mu.Unlock()

		if m := approvalURLPattern.FindString(line); m != "" {
			cp.setApprovalURL(m)
		}
	}
}

func (cp *ClientProcess) setApprovalURL(url string) {
	cp.urlOnce.Do(func() {
		cp.mu.Lock()
		cp.approvalURL = url
		cp.mu.Unlock()
		close(cp.urlSeen)
	})
}

// ApprovalURL blocks until the process has printed its approval URL (per
// ssh_login.go, always before it starts waiting on SSE), the process exits
// first, or timeout passes.
func (cp *ClientProcess) ApprovalURL(t *testing.T, timeout time.Duration) string {
	t.Helper()

	select {
	case <-cp.urlSeen:
		cp.mu.Lock()
		defer cp.mu.Unlock()
		return cp.approvalURL
	case <-cp.done:
		t.Fatalf("harness: the ssoossh client exited before printing an approval URL (%v)\nstderr:\n%s",
			cp.waitErr, cp.Stderr())
	case <-time.After(timeout):
		t.Fatalf("harness: timed out waiting for the ssoossh client to print an approval URL\nstderr so far:\n%s",
			cp.Stderr())
	}
	return ""
}

// Done returns a channel closed once the process has exited, for callers
// that want a non-blocking check (e.g. "assert it hasn't exited yet")
// rather than Wait's blocking one.
func (cp *ClientProcess) Done() <-chan struct{} { return cp.done }

// Wait blocks until the process exits, returning its error (nil on a clean
// exit). Fails the test if it doesn't exit before timeout.
func (cp *ClientProcess) Wait(t *testing.T, timeout time.Duration) error {
	t.Helper()

	select {
	case <-cp.done:
		return cp.waitErr
	case <-time.After(timeout):
		t.Fatalf("harness: the ssoossh client did not exit before timeout\nstderr so far:\n%s", cp.Stderr())
		return nil
	}
}

// ExitCode blocks until the process exits and returns its exit status.
// Fails the test if it doesn't exit before timeout.
func (cp *ClientProcess) ExitCode(t *testing.T, timeout time.Duration) int {
	t.Helper()

	// Wait's error is the non-zero exit itself, which is the very thing the
	// caller is asking this method for. Failing here would make the common
	// case — asserting that a bad invocation exits non-zero — impossible.
	cp.Wait(t, timeout) //nolint:errcheck // the exit status returned below is the answer being asked for

	if cp.cmd.ProcessState == nil {
		return -1
	}
	return cp.cmd.ProcessState.ExitCode()
}

// Stdout returns everything the process has written to stdout so far.
// Production code sends nothing here for `ssh login` — see ssh_login.go's
// doc comment — so this is mainly a sanity check that it stayed that way.
func (cp *ClientProcess) Stdout() string { return cp.stdout.String() }

// Stderr returns everything the process has written to stderr so far.
func (cp *ClientProcess) Stderr() string {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.stderr.String()
}

// RunLogout runs `ssoossh ssh logout` to completion and returns its
// combined output plus any error.
func RunLogout(t *testing.T, ssoosshPath, serverURL, agentSocket string) (output string, err error) {
	t.Helper()

	res := RunClient(t, ssoosshPath, ClientOptions{
		Args: []string{"ssh", "logout", "--server", serverURL},
		Env:  map[string]string{"SSH_AUTH_SOCK": agentSocket},
	})
	return res.Combined(), res.Err
}
