package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runHostPrincipals is what sshd invokes through AuthorizedPrincipalsCommand
// on every login attempt, as root, with no network. Its contract with sshd
// is unusual and worth pinning precisely: no output and exit 0 means "this
// account has no principals", so the only case that may fail loudly is a
// file that is there and unusable. Getting that backwards either denies
// every login on a host whose mapping is simply empty, or silently denies
// them on a host whose mapping is corrupt.

func TestRunHostPrincipals_ShouldPrintOnePrincipalPerLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte("deploy:\n  - alice\n  - bob\nother:\n  - carol\n"), 0o600); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runHostPrincipals(context.Background(), "deploy", path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	got := strings.Fields(out)
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Errorf("got %v, want [alice bob]", got)
	}
	// Another account's principals must not leak in. This is an
	// authorization boundary, not a formatting detail.
	if strings.Contains(out, "carol") {
		t.Errorf("another account's principal leaked into the answer:\n%s", out)
	}
}

func TestRunHostPrincipals_ShouldSucceedSilentlyWhenNothingMatches(t *testing.T) {
	dir := t.TempDir()
	populated := filepath.Join(dir, "principals.yaml")
	if err := os.WriteFile(populated, []byte("deploy:\n  - alice\n"), 0o600); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		account string
	}{
		{name: "unknown account", path: populated, account: "nobody"},
		{name: "missing file", path: filepath.Join(dir, "absent.yaml"), account: "deploy"},
		// An empty path is what an operator gets from `--file ""`. sshd
		// reads the empty answer as "no principals", which is the safe
		// reading of "no mapping was configured".
		{name: "no path at all", path: "", account: "deploy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := runHostPrincipals(context.Background(), tt.account, tt.path); err != nil {
					t.Fatalf("expected a silent success, got %v", err)
				}
			})

			if strings.TrimSpace(out) != "" {
				t.Errorf("expected no output, got %q", out)
			}
		})
	}
}

// A mapping file that is there and will not parse is the one case that must
// fail. Treating it as empty would silently deny every login on the host
// while everything looked healthy.
func TestRunHostPrincipals_ShouldFailWhenTheMappingIsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte("  deploy:\n"), 0o600); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	err := runHostPrincipals(context.Background(), "deploy", path)
	if err == nil {
		t.Fatal("expected a malformed mapping file to be an error")
	}
	if !strings.Contains(err.Error(), "parse principals map") {
		t.Errorf("got %q, want it to say the map would not parse", err.Error())
	}
}

// An unreadable file is not the same as a missing one: reporting it as
// "no principals" would hide a permissions mistake behind a denied login.
func TestRunHostPrincipals_ShouldFailWhenTheMappingCannotBeRead(t *testing.T) {
	// Windows has no POSIX permission bits to take the read away with:
	// os.Chmod there only toggles the read-only attribute, so a 0000 file
	// still reads back fine and the condition cannot be built. Nothing is
	// lost by skipping -- this command is sshd's
	// AuthorizedPrincipalsCommand, which is a Unix-only path.
	if runtime.GOOS == "windows" {
		t.Skip("windows cannot make a file unreadable through the file mode")
	}

	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte("deploy:\n  - alice\n"), 0o000); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can read a 0000 file")
	}

	err := runHostPrincipals(context.Background(), "deploy", path)
	if err == nil {
		t.Fatal("expected an unreadable mapping file to be an error")
	}
	if !strings.Contains(err.Error(), "read principals map") {
		t.Errorf("got %q, want it to name the read failure", err.Error())
	}
}

// captureStdout collects what fn writes to os.Stdout. runHostPrincipals
// prints with fmt.Println because sshd reads its stdout directly, so there
// is no writer to inject.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()

	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	return <-done
}
