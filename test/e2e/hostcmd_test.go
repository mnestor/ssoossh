//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// The host commands manage the local principal mapping, and none of them had
// ever been run as a process by a test. `host principals` matters most: sshd
// invokes it through AuthorizedPrincipalsCommand on every single login
// attempt, as root, and it must answer from local state without touching the
// network. A regression that made it reach the server would turn a server
// outage into an inability to log in anywhere.

// deadServer is an address nothing listens on. Every test in this file
// passes it rather than a real server, because that is the property being
// tested: these commands declare themselves offline, so root's PreRun must
// skip building an API client and fetching the CA entirely. Against a real
// server the assertion would be vacuous.
const deadServer = "http://127.0.0.1:1"

// runHost runs a host subcommand against mappingPath with no reachable
// server, returning the result for inspection.
func runHost(t *testing.T, bin, mappingPath string, args ...string) harness.ClientResult {
	t.Helper()

	full := append([]string{"host"}, args...)
	full = append(full, "--file", mappingPath, "--server", deadServer)
	return harness.RunClient(t, bin, harness.ClientOptions{Args: full})
}

// writeMapping writes a principals.yaml into a fresh directory and returns
// its path.
func writeMapping(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write mapping file: %v", err)
	}
	return path
}

func TestHostPrincipals_ShouldPrintMappedPrincipalsWhenTheAccountIsKnown(t *testing.T) {
	_, bin := harness.Binaries(t)
	mapping := writeMapping(t, "deploy:\n  - alice\n  - bob\nother:\n  - carol\n")

	res := runHost(t, bin, mapping, "principals", "deploy")

	if res.ExitCode != 0 {
		t.Fatalf("expected host principals to succeed, got exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	// One principal per line, which is the format sshd parses: the file's
	// own principals in file order, then the account name itself, which
	// this command guarantees whatever the mapping says.
	got := strings.Fields(res.Stdout)
	want := []string{"alice", "bob", "deploy"}
	if len(got) != len(want) {
		t.Fatalf("got principals %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("principal %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// carol belongs to a different account and must not leak into deploy's
	// answer -- this is an authorization boundary, not a formatting detail.
	if strings.Contains(res.Stdout, "carol") {
		t.Errorf("host principals leaked another account's principal:\n%s", res.Stdout)
	}
}

// An account the mapping does not cover still gets its own name, and
// nothing else. These must succeed rather than error: an unknown account
// erroring would deny every login on a host whose mapping simply has no
// entry yet, and printing nothing would leave an identity unable to use the
// one principal sshd would have accepted with no command configured at all.
func TestHostPrincipals_ShouldPrintTheAccountNameWhenNothingMatches(t *testing.T) {
	_, bin := harness.Binaries(t)

	tests := []struct {
		name    string
		mapping string
		missing bool
		account string
	}{
		{name: "unknown account", mapping: "deploy:\n  - alice\n", account: "nobody"},
		{name: "empty mapping", mapping: "", account: "deploy"},
		{name: "missing file", missing: true, account: "deploy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "principals.yaml")
			if !tt.missing {
				path = writeMapping(t, tt.mapping)
			}

			res := runHost(t, bin, path, "principals", tt.account)

			if res.ExitCode != 0 {
				t.Fatalf("expected exit 0, got %d\nstderr:\n%s", res.ExitCode, res.Stderr)
			}
			got := strings.Fields(res.Stdout)
			if len(got) != 1 || got[0] != tt.account {
				t.Errorf("got principals %v, want [%s]", got, tt.account)
			}
		})
	}
}

// A malformed file still answers, with the account name alone, and reports
// the breakage on stderr.
//
// It is not fail-closed on purpose: pam_ssoossh treats a map it cannot load
// as no map at all and falls back to requiring the certificate to carry the
// local account name. One file must not mean two policies, so refusing here
// while sudo still admitted the account would be worse than either
// behaviour alone. The file being broken is made loud in the log rather
// than in a denied login.
func TestHostPrincipals_ShouldFloorAndLogWhenTheMappingFileIsMalformed(t *testing.T) {
	_, bin := harness.Binaries(t)
	mapping := writeMapping(t, "  deploy:\n")

	res := runHost(t, bin, mapping, "principals", "deploy")

	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0 for a malformed mapping file, got %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if got, want := strings.Fields(res.Stdout), []string{"deploy"}; !slices.Equal(got, want) {
		t.Errorf("got principals %v, want %v", got, want)
	}
	// The parser's own wording is free to change; what must reach the
	// operator is that this file is why, so match the command's wrapper
	// rather than whichever syntax rule the file broke.
	if !strings.Contains(res.Stderr, "parse principals map") {
		t.Errorf("expected stderr to say the mapping would not parse, got:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, mapping) {
		t.Errorf("expected stderr to name the file, got:\n%s", res.Stderr)
	}
	// stdout is the principal list sshd parses; a diagnostic there would be
	// read as a principal.
	if strings.Contains(res.Stdout, "parse principals map") {
		t.Errorf("the diagnostic reached stdout, where sshd reads principals:\n%s", res.Stdout)
	}
}

func TestHostPrincipals_ShouldFailWhenNoUsernameIsGiven(t *testing.T) {
	_, bin := harness.Binaries(t)
	mapping := writeMapping(t, "deploy:\n  - alice\n")

	res := runHost(t, bin, mapping, "principals")

	if res.ExitCode == 0 {
		t.Fatal("expected a non-zero exit when no username is given, got 0")
	}
	if !strings.Contains(res.Stderr, "usage:") {
		t.Errorf("expected a usage message, got:\n%s", res.Stderr)
	}
}

// The round trip is the point: add, see it listed, remove, see it gone --
// through the real binary against a real file, rather than three unit tests
// of three functions that never meet.
func TestHostMapping_ShouldRoundTripAddListRemove(t *testing.T) {
	_, bin := harness.Binaries(t)
	mapping := filepath.Join(t.TempDir(), "principals.yaml")

	if res := runHost(t, bin, mapping, "mapping", "add", "deploy", "alice"); res.ExitCode != 0 {
		t.Fatalf("add alice failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res := runHost(t, bin, mapping, "mapping", "add", "deploy", "bob"); res.ExitCode != 0 {
		t.Fatalf("add bob failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	listed := runHost(t, bin, mapping, "mapping", "list")
	if listed.ExitCode != 0 {
		t.Fatalf("list failed with exit %d\nstderr:\n%s", listed.ExitCode, listed.Stderr)
	}
	for _, want := range []string{"deploy", "alice", "bob"} {
		if !strings.Contains(listed.Stdout, want) {
			t.Errorf("expected list output to contain %q, got:\n%s", want, listed.Stdout)
		}
	}

	// Removing one principal must leave the other. Removing the wrong
	// sibling, or the whole account, are both plausible bugs that a
	// "does it still parse" assertion would miss.
	if res := runHost(t, bin, mapping, "mapping", "remove", "deploy", "alice"); res.ExitCode != 0 {
		t.Fatalf("remove alice failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	after := runHost(t, bin, mapping, "mapping", "list")
	if strings.Contains(after.Stdout, "alice") {
		t.Errorf("alice survived removal:\n%s", after.Stdout)
	}
	if !strings.Contains(after.Stdout, "bob") {
		t.Errorf("bob was removed along with alice:\n%s", after.Stdout)
	}

	// `host principals` reads the same file, so it has to agree with list
	// on what the file says. These are the two halves of the feature and
	// nothing had checked they see the same thing.
	//
	// They are not identical, and deliberately so: `mapping list` reports
	// the file, while `host principals` answers sshd's question, which
	// includes the account's own name whether or not the file lists it. So
	// the assertion is that principals is list plus exactly that floor.
	principals := runHost(t, bin, mapping, "principals", "deploy")
	if got, want := strings.Fields(principals.Stdout), []string{"bob", "deploy"}; !slices.Equal(got, want) {
		t.Errorf("host principals disagrees with mapping list: got %v, want %v", got, want)
	}
}

func TestHostMapping_ShouldRemoveTheWholeAccountWhenGivenOneArgument(t *testing.T) {
	_, bin := harness.Binaries(t)
	mapping := writeMapping(t, "deploy:\n  - alice\n  - bob\nother:\n  - carol\n")

	if res := runHost(t, bin, mapping, "mapping", "remove", "deploy"); res.ExitCode != 0 {
		t.Fatalf("remove failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	after := runHost(t, bin, mapping, "mapping", "list")
	if strings.Contains(after.Stdout, "deploy") {
		t.Errorf("the deploy account survived a whole-account removal:\n%s", after.Stdout)
	}
	if !strings.Contains(after.Stdout, "carol") {
		t.Errorf("removing deploy also removed another account:\n%s", after.Stdout)
	}
}

// A missing file lists as nothing at all rather than a placeholder: the
// output is the file's own YAML, and the subset it is parsed from has no
// flow-mapping form, so a "{}" would not load back if redirected into one.
func TestHostMapping_ShouldPrintNothingWhenTheFileIsMissing(t *testing.T) {
	_, bin := harness.Binaries(t)
	missing := filepath.Join(t.TempDir(), "principals.yaml")

	res := runHost(t, bin, missing, "mapping", "list")

	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0 for a missing mapping file, got %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("expected no output, got %q", res.Stdout)
	}
}

func TestHostMapping_ShouldFailWhenGivenTooFewArguments(t *testing.T) {
	_, bin := harness.Binaries(t)

	tests := []struct {
		name string
		args []string
	}{
		{name: "add with no arguments", args: []string{"mapping", "add"}},
		{name: "add with only an account", args: []string{"mapping", "add", "deploy"}},
		{name: "remove with no arguments", args: []string{"mapping", "remove"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapping := writeMapping(t, "")

			res := runHost(t, bin, mapping, tt.args...)

			if res.ExitCode == 0 {
				t.Fatalf("expected a non-zero exit, got 0\nstdout:\n%s", res.Stdout)
			}
			if !strings.Contains(res.Stderr, "usage:") {
				t.Errorf("expected a usage message, got:\n%s", res.Stderr)
			}
		})
	}
}
