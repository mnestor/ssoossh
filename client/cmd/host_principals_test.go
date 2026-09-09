package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// runHostPrincipals is what sshd invokes through AuthorizedPrincipalsCommand
// on every login attempt, as root, with no network. Its contract with sshd
// is unusual and worth pinning precisely: the only case that may fail
// loudly is a file that is there and unusable, because treating a corrupt
// mapping as an empty one silently denies every login on the host.
//
// The account name itself is always among the principals printed. That is
// what sshd does unaided -- with no AuthorizedPrincipalsCommand configured
// it accepts a certificate carrying the target account name -- so
// installing this command no longer takes that away from an account whose
// mapping does not restate it. The floor applies on the success paths
// only; a file that will not load prints nothing at all.

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

	// The file's own order first, then the account name appended: anything
	// parsing this output sees the lines it saw before, in the same places.
	got := strings.Fields(out)
	want := []string{"alice", "bob", "deploy"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Another account's principals must not leak in. This is an
	// authorization boundary, not a formatting detail.
	if strings.Contains(out, "carol") {
		t.Errorf("another account's principal leaked into the answer:\n%s", out)
	}
}

// With nothing mapped for the account, the floor is the whole answer: the
// account name, once, and nothing else. This is the case that used to print
// nothing, which left an identity unable to use its own principal on a host
// whose mapping simply did not mention it.
func TestRunHostPrincipals_ShouldPrintTheAccountNameWhenNothingMatches(t *testing.T) {
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
		// An empty path is what an operator gets from `--file ""`: no
		// mapping was configured, so the floor is all there is to say.
		{name: "no path at all", path: "", account: "deploy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := runHostPrincipals(context.Background(), tt.account, tt.path); err != nil {
					t.Fatalf("expected a success, got %v", err)
				}
			})

			if got, want := strings.Fields(out), []string{tt.account}; !slices.Equal(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// A mapping that already lists the account gets no second copy, and keeps
// the order the file gave it.
func TestRunHostPrincipals_ShouldNotRepeatAnAlreadyMappedAccountName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte("deploy:\n  - alice\n  - deploy\n  - bob\n"), 0o600); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runHostPrincipals(context.Background(), "deploy", path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	got := strings.Fields(out)
	if want := []string{"alice", "deploy", "bob"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v (the file's order, untouched)", got, want)
	}
	if n := slices.Index(got[slices.Index(got, "deploy")+1:], "deploy"); n != -1 {
		t.Errorf("the account name was printed more than once: %v", got)
	}
}

// An account explicitly present with no principals still gets the floor.
// The mapping file can say "these principals and no others"; it can no
// longer say "not even your own name".
func TestRunHostPrincipals_ShouldFloorAnAccountMappedToNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte("deploy:\nother:\n  - alice\n"), 0o600); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runHostPrincipals(context.Background(), "deploy", path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if got, want := strings.Fields(out), []string{"deploy"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// An empty account name has nothing to floor with. sshd always passes %u,
// so this is a hand-run command with a blank argument, and a blank line is
// not a principal.
func TestRunHostPrincipals_ShouldNotPrintABlankLineForAnEmptyAccount(t *testing.T) {
	out := captureStdout(t, func() {
		if err := runHostPrincipals(context.Background(), "", ""); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if strings.TrimSpace(out) != "" || strings.Contains(out, "\n") {
		t.Errorf("expected no output at all, got %q", out)
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

	var err error
	out := captureStdout(t, func() {
		err = runHostPrincipals(context.Background(), "deploy", path)
	})
	if err == nil {
		t.Fatal("expected a malformed mapping file to be an error")
	}
	if !strings.Contains(err.Error(), "parse principals map") {
		t.Errorf("got %q, want it to say the map would not parse", err.Error())
	}
	// Not even the floor: sshd refuses the login on a non-zero exit either
	// way, and printing a usable principal while failing would invite
	// whoever reads the output to act on half an answer.
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output on the error path, got %q", out)
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
