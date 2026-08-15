//go:build e2e

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
)

// Binaries returns the paths to the built ssoosshd and ssoossh binaries,
// building them once per test run into a shared temp directory. Building
// here rather than relying on a separate CI step keeps
// `go test -tags=e2e ./test/e2e/...` runnable on its own.
//
// The build directory is deliberately not t.TempDir(): that would tie its
// lifetime to whichever test happens to trigger the sync.Once first, and
// every later test in the run needs it to still exist. It is left for the
// OS (or CI runner teardown) to reclaim instead.
func Binaries(t *testing.T) (ssoosshd, ssoossh string) {
	t.Helper()

	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ssoossh-e2e-bin-")
		if err != nil {
			buildErr = err
			return
		}
		buildDir = dir

		root, err := moduleRoot()
		if err != nil {
			buildErr = err
			return
		}

		if err := buildBinary(root, buildDir, "ssoosshd", "./cmd/ssoosshd"); err != nil {
			buildErr = err
			return
		}
		if err := buildBinary(root, buildDir, "ssoossh", "./cmd/ssoossh"); err != nil {
			buildErr = err
			return
		}
	})

	if buildErr != nil {
		t.Fatalf("harness: failed to build binaries: %v", buildErr)
	}

	return filepath.Join(buildDir, "ssoosshd"), filepath.Join(buildDir, "ssoossh")
}

// buildBinary runs `go build -o dir/name pkg` from root.
func buildBinary(root, dir, name, pkg string) error {
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &buildError{pkg: pkg, output: string(output), err: err}
	}
	return nil
}

type buildError struct {
	pkg    string
	output string
	err    error
}

func (e *buildError) Error() string {
	return "go build " + e.pkg + ": " + e.err.Error() + "\n" + strings.TrimSpace(e.output)
}

// moduleRoot returns the directory containing the module's go.mod, which is
// also the working directory `go build` needs (server/frontend embeds
// server/frontend/dist, which is only resolvable from the module root).
func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	return filepath.Dir(strings.TrimSpace(string(out))), nil
}
