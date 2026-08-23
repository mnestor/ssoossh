//go:build e2e

package harness

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	pamBuildOnce sync.Once
	pamBuildDir  string
	pamBuildErr  error
)

// PAMArtifacts returns paths to the built pam_ssoossh.so module and the
// compiled pamtest driver (pam_ssoossh/testing/pamtest.c), building both
// once per test run. Same lifetime rationale as Binaries: the directory is
// shared across tests, so it is not t.TempDir().
func PAMArtifacts(t *testing.T) (modulePath, pamtestPath string) {
	t.Helper()

	requirePAMBuildEnv(t)

	pamBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ssoossh-e2e-pam-")
		if err != nil {
			pamBuildErr = err
			return
		}
		pamBuildDir = dir

		root, err := moduleRoot()
		if err != nil {
			pamBuildErr = err
			return
		}

		// The module needs both the pam build tag (every file in the
		// package is behind it) and cgo — see pam_ssoossh/testing/README.md.
		module := exec.Command("go", "build", "-buildvcs=false", "-tags=pam",
			"-buildmode=c-shared", "-o", filepath.Join(dir, "pam_ssoossh.so"), "./pam_ssoossh/")
		module.Dir = root
		module.Env = append(os.Environ(), "CGO_ENABLED=1")
		if out, err := module.CombinedOutput(); err != nil {
			pamBuildErr = fmt.Errorf("build pam_ssoossh.so: %w\n%s", err, out)
			return
		}

		driver := exec.Command("gcc", "-o", filepath.Join(dir, "pamtest"),
			filepath.Join(root, "pam_ssoossh", "testing", "pamtest.c"), "-lpam", "-lpam_misc")
		if out, err := driver.CombinedOutput(); err != nil {
			pamBuildErr = fmt.Errorf("compile pamtest.c: %w\n%s", err, out)
		}
	})

	if pamBuildErr != nil {
		t.Fatalf("harness: failed to build PAM artifacts: %v", pamBuildErr)
	}

	return filepath.Join(pamBuildDir, "pam_ssoossh.so"), filepath.Join(pamBuildDir, "pamtest")
}

// requirePAMBuildEnv fails with actionable instructions if the PAM build
// toolchain is missing, rather than a cryptic cgo or gcc error mid-build.
// Mirrors requireSSHD: the tier that mutates the host also states its
// prerequisites.
func requirePAMBuildEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Fatalf("harness: gcc is not installed; run `sudo apt-get install -y gcc libpam0g-dev` (see docs/pam-e2e-testing.md)")
	}
	if _, err := os.Stat("/usr/include/security/pam_appl.h"); err != nil {
		t.Fatalf("harness: libpam development headers are missing; run `sudo apt-get install -y libpam0g-dev` (see docs/pam-e2e-testing.md)")
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Fatalf("harness: sudo is not available; the PAM stack test needs passwordless sudo to write /etc/pam.d")
	}
}

// InstallPAMService writes /etc/pam.d/<name> loading modulePath (by
// absolute path, so nothing is copied into the system module directory)
// against srv, removing the file in t.Cleanup. A dedicated service name so
// the real sudo/su stacks are never touched. The account stage is
// pam_permit: this tier's scope is the auth stage only.
func InstallPAMService(t *testing.T, name, modulePath string, srv *Server) {
	t.Helper()

	caFile := filepath.Join(t.TempDir(), "ca.pub")
	if err := os.WriteFile(caFile, []byte(strings.TrimSpace(srv.CAPublicKey)+"\n"), 0o644); err != nil { //nolint:gosec // a public key is not a secret
		t.Fatalf("harness: failed to write trusted-ca-file: %v", err)
	}

	// timeout=30s (down from the module's 60s default) bounds how long a
	// broken approval path stalls the suite; debug=stdout puts module logs
	// in the same capture as pamtest's own output.
	stanza := fmt.Sprintf(
		"auth    requisite   %s server=%s trusted-ca-file=%s debug=stdout timeout=30s\naccount required    pam_permit.so\n",
		modulePath, srv.BaseURL, caFile)

	path := filepath.Join("/etc/pam.d", name)
	tee := exec.Command("sudo", "tee", path)
	tee.Stdin = strings.NewReader(stanza)
	if out, err := tee.CombinedOutput(); err != nil {
		t.Fatalf("harness: failed to install %s: %v\n%s", path, err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("sudo", "rm", "-f", path).CombinedOutput(); err != nil {
			t.Logf("harness: failed to remove %s: %v\n%s", path, err, out)
		}
	})
}

// Pamtest is a running pamtest process: a real pam_start/pam_authenticate
// transaction against the installed service, blocked inside the module
// waiting for browser approval.
type Pamtest struct {
	cmd    *exec.Cmd
	output *lockedBuffer
	urlCh  chan string
	done   chan error
}

// StartPamtest launches pamtestPath against service and begins scanning its
// output for the approval URL the module surfaces through misc_conv.
func StartPamtest(t *testing.T, pamtestPath, service string) *Pamtest {
	t.Helper()

	p := &Pamtest{
		cmd:    exec.Command(pamtestPath, service),
		output: &lockedBuffer{},
		urlCh:  make(chan string, 1),
		done:   make(chan error, 1),
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("harness: failed to open pamtest stdout: %v", err)
	}
	p.cmd.Stderr = p.output

	if err := p.cmd.Start(); err != nil {
		t.Fatalf("harness: failed to start pamtest: %v", err)
	}

	// One goroutine owns stdout: capture every line, and hand the first
	// approval URL to urlCh. cmd.Wait must come after the pipe drains
	// (Wait closes the pipe), so it lives here too.
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			p.output.WriteLine(line)
			if strings.HasPrefix(line, "http") && strings.Contains(line, "/approve/") {
				select {
				case p.urlCh <- line:
				default:
				}
			}
		}
		p.done <- p.cmd.Wait()
	}()

	t.Cleanup(func() {
		if t.Failed() {
			writeArtifact(t, "pamtest-output.log", p.output.Bytes())
		}
		if p.cmd.ProcessState == nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill() //nolint:errcheck // best-effort teardown
		}
	})

	return p
}

// ApprovalURL blocks until the module prints the approval URL, failing the
// test if it doesn't appear in time.
func (p *Pamtest) ApprovalURL(t *testing.T) string {
	t.Helper()

	select {
	case u := <-p.urlCh:
		return u
	case err := <-p.done:
		t.Fatalf("harness: pamtest exited before printing an approval URL (%v)\noutput:\n%s", err, p.output.Bytes())
	case <-time.After(15 * time.Second):
		t.Fatalf("harness: pamtest did not print an approval URL in time\noutput:\n%s", p.output.Bytes())
	}
	return "" // unreachable; t.Fatalf above does not return
}

// Wait blocks until pamtest exits and returns its exit code and combined
// output. The deadline sits above the module's own 30s approval timeout, so
// a hang fails here with output rather than in the CI job's timeout.
func (p *Pamtest) Wait(t *testing.T) (exitCode int, output string) {
	t.Helper()

	select {
	case <-p.done:
		return p.cmd.ProcessState.ExitCode(), string(p.output.Bytes())
	case <-time.After(45 * time.Second):
		_ = p.cmd.Process.Kill() //nolint:errcheck // best-effort; the test is failing regardless
		t.Fatalf("harness: pamtest did not exit in time\noutput:\n%s", p.output.Bytes())
	}
	return 0, "" // unreachable; t.Fatalf above does not return
}

// lockedBuffer is a mutex-guarded byte buffer: pamtest's stderr (written by
// the OS pipe copier) and the stdout scanner goroutine both append to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) WriteLine(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(line)
	b.buf.WriteByte('\n')
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}
