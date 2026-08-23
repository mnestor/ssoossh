package signer_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestSignerHasNoDatabaseDependency enforces the constraint the package doc
// states: the signer must never gain database access, because that's what
// lets it become a separate, minimally-privileged process later (see
// docs/dev/signer-split-deferred.md).
//
// Asserted against the real dependency graph rather than by reading imports,
// so it also catches a database dependency arriving *transitively* — e.g. by
// importing server/service for a type — which is exactly how this boundary
// would realistically erode.
func TestSignerHasNoDatabaseDependency(t *testing.T) {
	t.Parallel()

	out, err := exec.Command("go", "list", "-deps", "github.com/mnestor/ssoossh/server/signer").Output()
	if err != nil {
		t.Fatalf("failed to list dependencies: %v", err)
	}

	forbidden := []string{
		"gorm.io/gorm",
		"github.com/mnestor/ssoossh/server/service",
	}
	for _, dep := range strings.Split(string(out), "\n") {
		for _, bad := range forbidden {
			if strings.TrimSpace(dep) == bad {
				t.Errorf("server/signer must not depend on %s — it has to stay database-free "+
					"so it can run as a separate, minimally-privileged process", bad)
			}
		}
	}
}
