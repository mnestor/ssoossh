//go:build e2e

package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// artifactsRoot is relative to the working directory `go test` uses, which
// is the directory of the package under test — test/e2e, the package that
// actually contains the *_test.go files, not this harness package (which
// has none). Artifacts land at test/e2e/_artifacts per the design doc.
const artifactsRoot = "_artifacts"

var nonArtifactChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// artifactDir returns (creating if needed) the directory a failing test
// writes its diagnostics to: test/e2e/_artifacts/<test-name>/.
func artifactDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(artifactsRoot, nonArtifactChars.ReplaceAllString(t.Name(), "_"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("harness: failed to create artifact directory %s: %v", dir, err)
		return ""
	}
	return dir
}

// writeArtifact best-effort writes data to name under t's artifact
// directory. Failing to write a diagnostic must not itself fail the test.
func writeArtifact(t *testing.T, name string, data []byte) {
	t.Helper()

	dir := artifactDir(t)
	if dir == "" {
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Logf("harness: failed to write artifact %s: %v", path, err)
	}
}
