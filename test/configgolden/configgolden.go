// Package configgolden compares a configuration file's effective values
// against a checked-in golden, ignoring everything about how the file is
// laid out or commented.
//
// It exists because client/config/defaults.yaml and
// server/config/defaults.yaml each do two jobs: they are the defaults
// embedded in the binary, and they are the commented configuration shipped
// to /etc/ssoossh (see .goreleaser.yml). That makes them prose files that
// happen to be executable, edited for their comments far more often than for
// their values. The golden separates the two — rewording a comment leaves it
// untouched, while activating a key, commenting one out, or changing a value
// fails loudly.
package configgolden

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Update is registered by this package so every test binary that uses it
// accepts the same `-update` flag as server/webtypes' goldens.
var Update = flag.Bool("update", false, "rewrite the golden files instead of comparing against them")

// Flatten renders parsed YAML as one sorted `dotted.key = value` line per
// leaf. Empty maps and sequences get their own line so that dropping the
// last element of a list is a visible change rather than a vanished key.
func Flatten(t *testing.T, doc string) string {
	t.Helper()

	var root map[string]any
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatalf("failed to parse the configuration: %v", err)
	}

	var lines []string
	var walk func(prefix string, node any)
	walk = func(prefix string, node any) {
		switch v := node.(type) {
		case map[string]any:
			if len(v) == 0 {
				lines = append(lines, prefix+" = {}")
				return
			}
			for key, child := range v {
				next := key
				if prefix != "" {
					next = prefix + "." + key
				}
				walk(next, child)
			}
		case []any:
			if len(v) == 0 {
				lines = append(lines, prefix+" = []")
				return
			}
			for i, child := range v {
				walk(fmt.Sprintf("%s[%d]", prefix, i), child)
			}
		default:
			lines = append(lines, fmt.Sprintf("%s = %v", prefix, v))
		}
	}
	walk("", root)

	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// Assert compares got against testdata/<name> relative to the calling
// package, or rewrites it when -update is set. pkg names the package in the
// `go test` command the failure suggests.
func Assert(t *testing.T, pkg, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)
	if *Update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatalf("failed to create testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("failed to write %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s (run `go test %s -update` to create it): %v", path, pkg, err)
	}
	if got != string(want) {
		t.Errorf("effective configuration changed.\n--- want (%s)\n%s\n--- got\n%s\n\n"+
			"Comment-only edits must not reach this golden. If the change is intended, "+
			"run `go test %s -update`.", path, want, got, pkg)
	}
}
