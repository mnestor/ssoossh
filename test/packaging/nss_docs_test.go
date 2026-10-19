package packaging_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// nssDocsPage is the user documentation's host page for ssoossh-nss. Its
// "nss.yaml reference" section documents every option with its default, in
// tables whose rows start with the option name and the default, each in
// backticks.
const nssDocsPage = "user-docs/src/content/docs/hosts/nss.md"

// nssDocsSection is the heading the reference tables sit under.
const nssDocsSection = "## nss.yaml reference"

// nssDocsRow is one option row: "| `name` | `default` | meaning |".
var nssDocsRow = regexp.MustCompile("^\\| `([a-z_]+)` \\| `([^`]*)` \\|")

// nssDocsOptions returns the documented options, name to default, and the
// names in the order the page lists them, from the reference section only.
func nssDocsOptions(t *testing.T) (map[string]string, []string) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), nssDocsPage))
	if err != nil {
		t.Fatalf("read %s: %v", nssDocsPage, err)
	}
	_, section, found := strings.Cut(string(body), "\n"+nssDocsSection+"\n")
	if !found {
		t.Fatalf("%s has no %q section", nssDocsPage, nssDocsSection)
	}
	// The section runs to the next second-level heading.
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	defaults := map[string]string{}
	var names []string
	for _, line := range strings.Split(section, "\n") {
		if m := nssDocsRow.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
			defaults[m[1]] = m[2]
		}
	}
	return defaults, names
}

// should document every nss.yaml option on the host page exactly once, so
// an option added to nssd/config cannot ship without a line in the docs.
func TestNSSDocsShouldListEveryOptionOnce(t *testing.T) {
	t.Parallel()

	_, names := nssDocsOptions(t)
	keys := nssConfigKeys()
	for _, key := range keys {
		if n := countOf(names, key); n != 1 {
			t.Errorf("%s lists %q %d times, want once", nssDocsPage, key, n)
		}
	}
	for _, name := range names {
		if !slices.Contains(keys, name) {
			t.Errorf("%s documents %q, which nss.yaml does not accept", nssDocsPage, name)
		}
	}
}

// should give every option the default the shipped example gives it. The
// example is itself checked against the daemon's defaults, so the page, the
// example and the binary cannot drift apart.
func TestNSSDocsShouldMatchTheExampleDefaults(t *testing.T) {
	t.Parallel()

	defaults, _ := nssDocsOptions(t)
	options, _ := nssExampleOptions(t)
	for _, line := range strings.Split(strings.TrimSpace(options), "\n") {
		name, value, _ := strings.Cut(line, ":")
		want := strings.TrimSpace(value)
		got, ok := defaults[name]
		if !ok {
			// TestNSSDocsShouldListEveryOptionOnce reports it.
			continue
		}
		if got != want {
			t.Errorf("%s documents %s as %q, the shipped example as %q", nssDocsPage, name, got, want)
		}
	}
}
