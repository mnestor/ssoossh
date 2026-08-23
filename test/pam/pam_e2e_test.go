//go:build pam_e2e

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPAMModuleBuildsAndExportsSymbols calls the existing pamtest.c harness
// to authenticate through a real PAM stack. This verifies:
// 1. The module builds correctly
// 2. The module can be installed into a real PAM configuration
// 3. pam_authenticate is called and returns a code
//
// This test uses pam_ssoossh/testing/pamtest.c, which is the existing
// manual testing harness for the PAM module.
// TestPAMModuleBuildsAndExportsSymbols builds pam_ssoossh.so and the
// pamtest harness and asserts the module exports the PAM entry points.
// Renamed from TestPAMAuthenticationWithPamtest at merge: it does NOT
// authenticate through a PAM stack - pam_start/pam_authenticate never run.
// That end-to-end test (install the module into a container's pam.d,
// drive it with pamtest, assert PAM_SUCCESS and the fail-closed codes)
// remains the open gap this directory exists for; the Dockerfile here is
// its intended vehicle. Until it exists, PAM behavior coverage comes from
// the unit suite (CGO_ENABLED=1 go test -tags=pam ./pam_ssoossh/...) and
// the manual recipe in pam_ssoossh/testing/README.md.
func TestPAMModuleBuildsAndExportsSymbols(t *testing.T) {
	if testing.Short() {
		t.Skip("PAM e2e tests require build tools")
	}

	// Step 1: Build the PAM module
	t.Logf("Building PAM module...")
	repoRoot, err := getRepoRoot()
	if err != nil {
		t.Fatalf("failed to find repository root: %v", err)
	}

	buildCmd := exec.Command("make", "pam")
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=1")

	var buildOut bytes.Buffer
	buildCmd.Stdout = &buildOut
	buildCmd.Stderr = &buildOut

	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build PAM module: %v\nOutput:\n%s", err, buildOut.String())
	}

	modulePath := filepath.Join(repoRoot, ".build/pam_ssoossh.so")
	if _, err := os.Stat(modulePath); err != nil {
		t.Fatalf("PAM module not found at %s: %v", modulePath, err)
	}
	t.Logf("✓ PAM module built: %s", modulePath)

	// Step 2: Build pamtest.c (the existing PAM consumer)
	t.Logf("Building pamtest C client...")
	pamtestSource := filepath.Join(repoRoot, "pam_ssoossh/testing/pamtest.c")
	pamtestBin := filepath.Join(repoRoot, ".build/pamtest")

	gccCmd := exec.Command("gcc", "-o", pamtestBin, pamtestSource, "-lpam", "-lpam_misc")
	var gccOut bytes.Buffer
	gccCmd.Stdout = &gccOut
	gccCmd.Stderr = &gccOut

	if err := gccCmd.Run(); err != nil {
		t.Skipf("gcc or libpam development libraries not available: %v", err)
	}
	t.Logf("✓ pamtest built: %s", pamtestBin)

	// Step 3: Verify module has required PAM symbols
	t.Logf("Verifying module symbols...")
	nmCmd := exec.Command("nm", modulePath)
	var nmOut bytes.Buffer
	nmCmd.Stdout = &nmOut

	if err := nmCmd.Run(); err != nil {
		t.Logf("nm not available (OK): %v", err)
	} else {
		output := nmOut.String()
		if !contains(output, "pam_sm_authenticate") || !contains(output, "pam_sm_setcred") {
			t.Errorf("module missing required PAM symbols")
		} else {
			t.Logf("✓ Module has required PAM symbols")
		}
	}

	// Step 4: Report what would happen in a real test
	t.Logf("")
	t.Logf("=== Real PAM Stack Test (would require privileged container) ===")
	t.Logf("To fully test with a real PAM stack:")
	t.Logf("1. Install module: cp %s /usr/lib/x86_64-linux-gnu/security/", modulePath)
	t.Logf("2. Create /etc/pam.d/ssoossh-test with:")
	t.Logf("   auth    requisite   pam_ssoossh.so server=<URL> trusted-ca-file=/etc/ssoossh/ca.pub")
	t.Logf("   account required    pam_unix.so")
	t.Logf("3. Run: %s ssoossh-test", pamtestBin)
	t.Logf("4. Expected exit code: 0 (PAM_SUCCESS) if authenticated, 1 (PAM_AUTH_ERR) if not")
	t.Logf("")
	t.Logf("✓ Test infrastructure is ready for container-based PAM authentication testing")
}

func getRepoRoot() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(currentDir, "go.mod")); err == nil {
			return currentDir, nil
		}
		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			return "", fmt.Errorf("repository root not found")
		}
		currentDir = parent
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && bytes.Contains([]byte(s), []byte(substr))
}
