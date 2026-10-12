//go:build e2e || resilience || load

package harness

import (
	"fmt"
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

// prebuiltDirEnv names a directory that already holds ssoosshd and ssoossh
// built from this checkout. CI's e2e prepare job builds both once and hands
// them to the tiers, and without this the first test of every tier built
// them again, on its own clock: a go build with whatever the restored cache
// happened to hold, 5s warm and about a minute cold, which is how
// TestAccount_ShowsExtraFieldsAndMergedPrincipals (the package's first
// test) came to take 49-78s. A relative path is taken from the module root.
// Unset, the local default, the harness builds its own, so a local run
// never picks up a stale bin/ by accident.
const prebuiltDirEnv = "SSOOSSH_E2E_BIN_DIR"

// Binaries returns the paths to the built ssoosshd and ssoossh binaries,
// building them once per test run into a shared temp directory, or using
// the ones in $SSOOSSH_E2E_BIN_DIR when that is set (see prebuiltDirEnv).
// Building here rather than relying on a separate CI step keeps
// `go test -tags=e2e ./test/e2e/...` runnable on its own.
//
// The build directory is deliberately not t.TempDir(): that would tie its
// lifetime to whichever test happens to trigger the sync.Once first, and
// every later test in the run needs it to still exist. It is left for the
// OS (or CI runner teardown) to reclaim instead.
func Binaries(t *testing.T) (ssoosshd, ssoossh string) {
	t.Helper()

	buildOnce.Do(func() {
		if dir := os.Getenv(prebuiltDirEnv); dir != "" {
			buildDir, buildErr = prebuiltBinaries(dir)
			return
		}

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

// prebuiltBinaries checks that dir holds executable ssoosshd and ssoossh
// files and returns dir as an absolute path, resolving a relative dir
// against the module root (go test runs each package from its own
// directory, so the working directory would make "bin" mean test/e2e/bin).
//
// A missing or non-executable binary is an error, never a quiet rebuild:
// the variable says the build happened elsewhere, and building anyway
// would hide a broken hand-off behind a run that is merely slow again.
func prebuiltBinaries(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		root, err := moduleRoot()
		if err != nil {
			return "", fmt.Errorf("%s=%q: find the module root: %w", prebuiltDirEnv, dir, err)
		}
		dir = filepath.Join(root, dir)
	}

	for _, name := range []string{"ssoosshd", "ssoossh"} {
		info, err := os.Stat(filepath.Join(dir, name)) //nolint:gosec // dir is the operator's own SSOOSSH_E2E_BIN_DIR and name a constant: reading where it points is the point
		if err != nil {
			return "", fmt.Errorf("%s: %w", prebuiltDirEnv, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return "", fmt.Errorf("%s: %s is not an executable file", prebuiltDirEnv, filepath.Join(dir, name))
		}
	}
	return dir, nil
}

// buildBinary runs `go build -o dir/name pkg` from root.
//
// CGO_ENABLED=1 for the same reason the Makefile's build target sets it:
// ssoosshd reaches libpkcs11 through crypto11, and inheriting a cgo-free
// environment (the devcontainer default) would fail every test at harness
// setup.
func buildBinary(root, dir, name, pkg string) error {
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out, pkg)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
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
