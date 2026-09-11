package cmd

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/version"
)

// Test methodology: writeVersion takes an io.Writer, so both forms are
// asserted against a bytes.Buffer with no process stdout involved. The
// capability list is checked against the real command tree rather than
// against a second copy of itself, so the test fails when a command is
// added or renamed without the contract being updated.

func TestWriteVersion_ShouldPrintTheHumanFormWhenJSONIsNotRequested(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeVersion(&out, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := version.Name + " " + version.Version
	if got := out.String(); !strings.HasPrefix(got, want) {
		t.Errorf("got %q, want it to start with %q", got, want)
	}
}

func TestWriteVersion_ShouldNotPrintJSONWhenJSONIsNotRequested(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeVersion(&out, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if strings.Contains(out.String(), "capabilities") {
		t.Errorf("the human form leaked JSON fields: %s", out.String())
	}
}

func TestWriteVersion_ShouldPrintTheBuildFieldsWhenJSONIsRequested(t *testing.T) {
	t.Parallel()

	got := decodeVersionJSON(t)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "name", got: got.Name, want: version.Name},
		{name: "version", got: got.Version, want: version.Version},
		{name: "commit", got: got.Commit, want: version.Commit},
		{name: "date", got: got.Date, want: version.Date},
		{name: "built_by", got: got.BuiltBy, want: version.BuiltBy},
	}
	for _, tt := range tests {
		t.Run("should report "+tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestWriteVersion_ShouldReportCapabilitiesWhenJSONIsRequested(t *testing.T) {
	t.Parallel()

	if got := decodeVersionJSON(t).Capabilities; len(got) == 0 {
		t.Error("expected a non-empty capability list")
	}
}

// The list is sorted so that a deployment tool diffing two releases sees
// only real additions and removals, never a reordering.
func TestVersion_CapabilitiesShouldBeSorted(t *testing.T) {
	t.Parallel()

	got := capabilities()
	if !slices.IsSorted(got) {
		t.Errorf("capabilities are not sorted: %v", got)
	}
}

// TestVersion_CapabilitiesShouldMatchTheCommandTree is the guard that keeps
// the hand-written list honest. It walks the real tree and compares leaf
// command paths both ways, so a new command that nobody declared and a
// declared capability that no longer exists are each a failure.
func TestVersion_CapabilitiesShouldMatchTheCommandTree(t *testing.T) {
	t.Parallel()

	root, err := newManpageRoot()
	if err != nil {
		t.Fatalf("build command tree: %v", err)
	}
	if root.cobraRoot == nil {
		t.Fatal("command tree compiled without capturing the cobra root")
	}

	var leaves []string
	collectLeafPaths(root.cobraRoot, nil, &leaves)
	slices.Sort(leaves)

	declared := capabilities()

	for _, leaf := range leaves {
		if !slices.Contains(declared, leaf) {
			t.Errorf("command %q exists but is not declared in capabilities()", leaf)
		}
	}
	for _, c := range declared {
		if !slices.Contains(leaves, c) {
			t.Errorf("capability %q is declared but no such command exists", c)
		}
	}
}

// collectLeafPaths appends the space-joined path of every command with no
// subcommands of its own, which is the form capabilities() uses. The root
// itself is skipped: its name is the binary, not a capability.
func collectLeafPaths(cmd *cobra.Command, prefix []string, out *[]string) {
	children := cmd.Commands()
	if len(children) == 0 {
		if len(prefix) > 0 {
			*out = append(*out, strings.Join(prefix, " "))
		}
		return
	}
	for _, child := range children {
		collectLeafPaths(child, append(slices.Clone(prefix), child.Name()), out)
	}
}

// decodeVersionJSON renders the JSON form and decodes it, failing the test
// if it is not valid JSON.
func decodeVersionJSON(t *testing.T) versionInfo {
	t.Helper()

	var out bytes.Buffer
	if err := writeVersion(&out, true); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var got versionInfo
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (output: %s)", err, out.String())
	}
	return got
}
