package cmd

// Test methodology: relayChild runs a real child, this test binary run
// again as a stand-in relay (TestRelayChildHelper), so the same tests hold
// on the Windows runner, where relayChild is the hand-off. What matters: the
// child receives argv whole and the stdio it was given, a failing child
// comes back as its exit status, and the hand-off finds a bare command name
// on PATH before trying to run it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"
)

// relayHelperEnv marks a run of this test binary as the stand-in relay.
const relayHelperEnv = "SSOOSSH_TEST_RELAY_HELPER"

// TestRelayChildHelper is the stand-in relay rather than a test, and does
// nothing unless relayChild started it with relayHelperEnv set. It prints
// each argument after "--" on its own line, copies stdin to stdout, then
// exits with the status the first of those arguments names.
func TestRelayChildHelper(t *testing.T) {
	if os.Getenv(relayHelperEnv) != "1" {
		return
	}

	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	for _, arg := range args {
		fmt.Println(arg)
	}
	if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
		os.Exit(100)
	}
	code, err := strconv.Atoi(args[0])
	if err != nil {
		os.Exit(101)
	}
	os.Exit(code)
}

// helperRelay returns the path, argv and environment that start the
// stand-in relay with args, the first of which is its exit status.
func helperRelay(t *testing.T, args ...string) (path string, argv, env []string) {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	argv = append([]string{exe, "-test.run=^TestRelayChildHelper$", "--"}, args...)
	return exe, argv, append(os.Environ(), relayHelperEnv+"=1")
}

func TestRelayChild_ShouldGiveTheChildItsArgvAndStdio(t *testing.T) {
	path, argv, env := helperRelay(t, "0", "-", "TCP:db01.internal:22")
	var stdout bytes.Buffer

	if err := relayChild(path, argv, env, strings.NewReader("ssh stream"), &stdout, io.Discard); err != nil {
		t.Fatalf("relayChild() error = %v", err)
	}

	want := "0\n-\nTCP:db01.internal:22\nssh stream"
	if got := strings.ReplaceAll(stdout.String(), "\r\n", "\n"); got != want {
		t.Errorf("child wrote %q, want %q", got, want)
	}
}

func TestRelayChild_ShouldReturnTheChildsExitStatus(t *testing.T) {
	path, argv, env := helperRelay(t, "3")

	err := relayChild(path, argv, env, strings.NewReader(""), io.Discard, io.Discard)

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("relayChild() error = %v, want exit status 3", err)
	}
}

// execve and CreateProcess do no PATH lookup, so a bare "nc" failed with
// ENOENT. The lookup has to happen first; ErrNotFound is its error alone.
func TestHandOff_ShouldLookABareCommandUpOnPath(t *testing.T) {
	err := handOff("ssoossh-test-no-such-relay", []string{"ssoossh-test-no-such-relay"}, os.Environ())

	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("handOff() error = %v, want exec.ErrNotFound from the PATH lookup", err)
	}
}

// A relay that ran and failed has already explained itself on stderr; the
// error says it exited rather than that it could not be run.
func TestProxyCommand_ShouldReportTheRelaysExitStatus(t *testing.T) {
	ours := newTestCA(t)
	valid := newTestCert(t, ours, "alice", time.Hour)
	ag := &stubAgent{identities: []xssh.PublicKey{valid}, cas: []xssh.PublicKey{ours.public}}

	path, argv, env := helperRelay(t, "3")
	original := execCommand
	execCommand = func(string, []string, []string) error {
		return relayChild(path, argv, env, strings.NewReader(""), io.Discard, io.Discard)
	}
	t.Cleanup(func() { execCommand = original })

	x := newProxyExec(t, &fakeAPIClient{}, ag)
	_, err := x.Execute(context.Background(), []string{"ssh", "proxycommand", "nc", "host", "22"})

	if err == nil || !strings.Contains(err.Error(), `the proxy command "nc" exited with status 3`) {
		t.Errorf("got %v, want the relay's exit status reported", err)
	}
}
