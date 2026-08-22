//go:build resilience

package resilience

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// fixture holds a running server, IdP, and client infrastructure for resilience tests.
// It shares the same harness as e2e tests, reusing Binaries, StartServer, etc.
type fixture struct {
	idp       *harness.IdentityProvider
	server    *harness.Server
	agent     *harness.Agent
	browser   *harness.Browser
	ssoosshBin string
	ssoosshd  string
}

// newFixture starts a minimal harness: IdP, server, and agent. It does not start
// a browser or login flow — callers control those to inject failures at specific points.
func newFixture(t *testing.T) *fixture {
	t.Helper()

	ssoosshd, ssoossh := harness.Binaries(t)
	idp := harness.NewIdentityProvider(t)
	server := harness.StartServer(t, idp, harness.ServerOptions{
		// Longer lifetime than default 30s so tests don't race against expiration
		ValidDuration: "8h",
	})
	agent := harness.StartAgent(t)

	return &fixture{
		idp:        idp,
		server:     server,
		agent:      agent,
		ssoosshBin: ssoossh,
		ssoosshd:   ssoosshd,
	}
}

// startBrowser lazily starts a browser on first call; subsequent calls return
// the same instance. Cleanup is automatic via t.Cleanup.
func (f *fixture) startBrowser(t *testing.T) *harness.Browser {
	t.Helper()
	if f.browser == nil {
		f.browser = harness.StartBrowser(t)
	}
	return f.browser
}

// contextWithTimeout returns a context cancelled after d. Used for tests that
// need to verify timeout handling.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// waitFor is a sensible default timeout for blocking operations (login, approval, etc).
const waitFor = 10 * time.Second

// environment returns the current environment, for tests that need to fork
// processes with specific env vars (e.g., to disable pubsub or database).
func environment() []string {
	return os.Environ()
}
