//go:build e2e || resilience || load

package harness

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestSSHUser is the dedicated local account tier 3 maps certificate
// principals onto. A dedicated account rather than reusing whoever is
// running the tests, so the harness's account changes (unlocking password
// auth) are scoped to something the harness owns.
const TestSSHUser = "ssoossh-e2e"

// SSHD is a running sshd subprocess, trusting a harness-generated CA and
// mapping its one certificate principal (TestSSHUser) onto a real local
// account.
type SSHD struct {
	// Port is the loopback port sshd is listening on.
	Port int
	// Principal is the certificate principal sshd will accept — always
	// TestSSHUser.
	Principal string

	cmd            *exec.Cmd
	stdout, stderr *lockedBuffer
}

// StartSSHD ensures a dedicated unlocked local account exists, writes a
// per-run sshd_config trusting caPublicKey (authorized_keys format) for
// TestSSHUser, and launches a real sshd as root via sudo. Requires sshd to
// be installed (`apt-get install openssh-server`) and passwordless sudo —
// both true on the hosted CI runner this targets; see the "sshd needs an
// unlocked account" obstacle in docs/dev/e2e-testing-plan.md.
func StartSSHD(t *testing.T, caPublicKey string) *SSHD {
	t.Helper()

	requireSSHD(t)
	ensureTestUser(t)

	// sshd reads AuthorizedPrincipalsFile (and TrustedUserCAKeys) after
	// temporarily dropping privileges to the *target* account's uid, not as
	// root. t.TempDir() is unusable here: it nests the returned directory
	// under a per-test parent that Go itself creates 0700, and that parent
	// blocks the target account from ever traversing down to a chmodded
	// leaf — which fails closed as a silent "no principal file" rather than
	// a permission error. A directory created directly under the system
	// temp root (itself world-traversable) has no such parent.
	dir, err := os.MkdirTemp("", "ssoossh-e2e-sshd-")
	if err != nil {
		t.Fatalf("harness: failed to create sshd config directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// World-readable/traversable is fine: everything under dir is test
	// fixture material, never a secret except the host key, written 0600
	// below regardless.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("harness: failed to make sshd config directory traversable: %v", err)
	}
	port := freePort(t)

	hostKeyPath := filepath.Join(dir, "host_key")
	writeHostKey(t, hostKeyPath)

	caFile := filepath.Join(dir, "ca.pub")
	// 0644, not 0600: sshd reads TrustedUserCAKeys after dropping privileges
	// to the *target* account (see the dir comment above) — a public key
	// file world-readable is not a secret, and 0600 would reproduce the
	// exact "silent no principal file" failure that comment describes.
	if err := os.WriteFile(caFile, []byte(strings.TrimSpace(caPublicKey)+"\n"), 0o644); err != nil { //nolint:gosec // must be readable by the target account sshd drops to
		t.Fatalf("harness: failed to write TrustedUserCAKeys file: %v", err)
	}

	principalsFile := filepath.Join(dir, "principals")
	if err := os.WriteFile(principalsFile, []byte(TestSSHUser+"\n"), 0o644); err != nil { //nolint:gosec // must be readable by the target account sshd drops to, see caFile above
		t.Fatalf("harness: failed to write AuthorizedPrincipalsFile: %v", err)
	}

	configPath := filepath.Join(dir, "sshd_config")
	config := fmt.Sprintf(`Port %d
ListenAddress 127.0.0.1
HostKey %s
TrustedUserCAKeys %s
AuthorizedPrincipalsFile %s
PasswordAuthentication no
KbdInteractiveAuthentication no
UsePAM no
StrictModes no
LogLevel VERBOSE
`, port, hostKeyPath, caFile, principalsFile)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("harness: failed to write sshd_config: %v", err)
	}

	cmd := exec.Command("sudo", "/usr/sbin/sshd", "-f", configPath, "-D", "-e")
	var stdout, stderr lockedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: failed to start sshd: %v", err)
	}

	s := &SSHD{Port: port, Principal: TestSSHUser, cmd: cmd, stdout: &stdout, stderr: &stderr}
	t.Cleanup(func() { s.shutdown(t) })
	s.waitReady(t)

	return s
}

func (s *SSHD) waitReady(t *testing.T) {
	t.Helper()

	addr := fmt.Sprintf("127.0.0.1:%d", s.Port)
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if s.cmd.ProcessState != nil {
			t.Fatalf("harness: sshd exited before becoming ready (%v)\nstderr:\n%s", s.cmd.ProcessState, s.stderr.String())
		}
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("harness: sshd did not become ready before deadline: %v\nstderr:\n%s", lastErr, s.stderr.String())
}

// shutdown stops sshd via sudo (it forked as root, so this process's own
// signal would not reach it) and captures logs on failure.
func (s *SSHD) shutdown(t *testing.T) {
	t.Helper()

	if t.Failed() {
		writeArtifact(t, "sshd-stdout.log", s.stdout.Bytes())
		writeArtifact(t, "sshd-stderr.log", s.stderr.Bytes())
	}

	if s.cmd.Process == nil {
		return
	}

	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()

	_ = exec.Command("sudo", "kill", "-TERM", fmt.Sprint(s.cmd.Process.Pid)).Run() //nolint:errcheck,gosec // best-effort graceful shutdown; pid is this process's own child, not external input

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = exec.Command("sudo", "kill", "-KILL", fmt.Sprint(s.cmd.Process.Pid)).Run() //nolint:errcheck,gosec // best-effort teardown; pid is this process's own child, not external input
		<-done
	}
}

// requireSSHD fails with actionable instructions if sshd isn't installed,
// rather than a confusing "no such file" from exec.Command.
func requireSSHD(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("/usr/sbin/sshd"); err != nil {
		t.Fatalf("harness: sshd is not installed; run `sudo apt-get install -y openssh-server` (see docs/dev/e2e-testing-plan.md, tier 3)")
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Fatalf("harness: sudo is not available; tier 3 needs passwordless sudo to run sshd as root")
	}
}

// ensureTestUser creates TestSSHUser (home directory, unlocked password
// auth slot) if it doesn't already exist. Idempotent, so repeated local
// runs don't fail on the second one.
func ensureTestUser(t *testing.T) {
	t.Helper()

	if _, err := user.Lookup(TestSSHUser); err == nil {
		return
	}

	// Lookup-then-create is not atomic, and the account is host state: two
	// e2e runs starting together both miss the lookup and both call
	// useradd, and the loser gets exit 9 ("user already exists"). That is a
	// success for our purposes -- the account we needed is there -- so it
	// is treated as one rather than failing a run for winning a race it
	// should not have been in. Any other failure is still fatal.
	if out, err := exec.Command("sudo", "useradd", "-m", "-s", "/bin/sh", TestSSHUser).CombinedOutput(); err != nil {
		if _, lookupErr := user.Lookup(TestSSHUser); lookupErr != nil {
			t.Fatalf("harness: failed to create test user %s: %v\n%s", TestSSHUser, err, out)
		}
	}
	// A password-locked account is rejected by sshd before any key is even
	// considered ("User ... not allowed because account is locked"), which
	// looks like a certificate problem and isn't one. This clears the lock
	// without setting a usable password — public-key-only auth is enforced
	// by sshd_config (PasswordAuthentication no) regardless.
	if out, err := exec.Command("sudo", "usermod", "-p", "*", TestSSHUser).CombinedOutput(); err != nil {
		t.Fatalf("harness: failed to unlock test user %s: %v\n%s", TestSSHUser, err, out)
	}
}

// writeHostKey generates a fresh ed25519 host key and writes it to path.
func writeHostKey(t *testing.T, path string) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("harness: failed to generate sshd host key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "e2e-test-host-key")
	if err != nil {
		t.Fatalf("harness: failed to marshal sshd host key: %v", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("harness: failed to write sshd host key: %v", err)
	}
}

// RunSSH runs the real ssh client against sshd, authenticating with
// whatever certificate is loaded in the agent at agentSocket, and returns
// its combined output plus any error.
func RunSSH(t *testing.T, s *SSHD, agentSocket string, remoteCommand string) (output string, err error) {
	t.Helper()

	return RunSSHWith(t, s, agentSocket, nil, remoteCommand)
}

// RunSSHWith is RunSSH with extra -o options, for tests that need ssh
// itself configured differently -- ProxyCommand being the one that matters,
// since that is how `ssoossh ssh proxycommand` is reached in production and
// there is no other way to drive it as ssh actually invokes it.
//
// Each entry in extraOptions is one option string, e.g.
// "ProxyCommand /usr/bin/ssoossh ssh proxycommand /usr/bin/nc %h %p".
func RunSSHWith(t *testing.T, s *SSHD, agentSocket string, extraOptions []string, remoteCommand string) (output string, err error) {
	t.Helper()

	args := []string{
		// -F /dev/null: ignore the developer's own ssh config (and with it
		// the system-wide one) — a personal "Host *" with IdentitiesOnly or
		// a fixed IdentityFile would silently stop ssh from offering the
		// agent's certificate, failing this tier only on that machine.
		"-F", "/dev/null",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=no",
		"-o", "PreferredAuthentications=publickey",
		"-o", "PasswordAuthentication=no",
		"-p", fmt.Sprint(s.Port),
		s.Principal + "@127.0.0.1",
		"--",
		remoteCommand,
	}
	// Extra options go before the destination, and are inserted rather than
	// appended: everything from s.Principal onwards is positional.
	if len(extraOptions) > 0 {
		withOpts := make([]string, 0, len(args)+2*len(extraOptions))
		for _, opt := range extraOptions {
			withOpts = append(withOpts, "-o", opt)
		}
		args = append(withOpts, args...)
	}

	cmd := exec.Command("ssh", args...)
	// HOME is isolated for the same reason the client harness isolates it:
	// a ProxyCommand runs the ssoossh client as a child of ssh, inheriting
	// this environment, and without this it would read the developer's own
	// ~/.config/ssoossh.yaml. ssh itself needs no home here -- -F /dev/null
	// and UserKnownHostsFile=/dev/null already cover what it would look for.
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+agentSocket, "HOME="+t.TempDir())

	out, runErr := cmd.CombinedOutput()
	return string(out), runErr
}
