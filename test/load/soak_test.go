//go:build load || e2e

package load

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// defaultSoakDuration is how long each sustained-load test runs by default.
// It is deliberately short: the whole package shares one `go test -timeout`,
// and a soak long enough to be worth the name would blow it. Set
// SSOOSSH_SOAK_DURATION (any time.ParseDuration value, e.g. "30m") to run a
// real soak locally or on a schedule with a matching -timeout.
const defaultSoakDuration = 30 * time.Second

// soakDurationEnv names the override, kept in one place so the skip message
// and the parser cannot drift apart.
const soakDurationEnv = "SSOOSSH_SOAK_DURATION"

// TestSoak_SustainedLoad_ThreeWorkers exercises the server under continuous
// load from three workers, watching for gradual degradation: connection
// pool exhaustion, stale connections, a server that stops serving.
func TestSoak_SustainedLoad_ThreeWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping soak test in short mode")
	}

	testSoakLoad(t, soakDuration(t), 3)
}

// TestSoak_SustainedLoad_FiveWorkers is the same soak at higher concurrency.
func TestSoak_SustainedLoad_FiveWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping soak test in short mode")
	}

	testSoakLoad(t, soakDuration(t), 5)
}

// soakDuration returns the configured soak length, defaulting to something
// that fits a normal CI run.
func soakDuration(t *testing.T) time.Duration {
	t.Helper()

	raw := os.Getenv(soakDurationEnv)
	if raw == "" {
		return defaultSoakDuration
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("%s=%q is not a duration: %v", soakDurationEnv, raw, err)
	}
	return d
}

func testSoakLoad(t *testing.T, duration time.Duration, concurrency int) {
	t.Helper()

	idp := harness.NewIdentityProvider(t)
	server := harness.StartServer(t, idp, harness.ServerOptions{})
	agent := harness.StartAgent(t)
	_, ssoossh := harness.Binaries(t)

	var totalRequests int64
	var totalSuccesses int64
	var totalFailures int64

	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup

	// Spawn N concurrent workers that repeatedly issue logins.
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()

			for iteration := 0; time.Now().Before(deadline); iteration++ {
				atomic.AddInt64(&totalRequests, 1)

				// --force every time: the workers share one agent, and
				// without it the client finds the certificate a previous
				// iteration left there, reuses it, and exits before it ever
				// registers a request — no load, and no approval URL.
				login := harness.StartLogin(t, ssoossh, server.BaseURL, agent.Socket, "--force")
				url := login.ApprovalURL(t, waitFor)

				// A distinct user per iteration, so each request is its own.
				username := fmt.Sprintf("worker%d_user%d", w, iteration)
				if err := approveAs(server.BaseURL, url, username); err != nil {
					atomic.AddInt64(&totalFailures, 1)
					t.Logf("approving %s failed: %v", username, err)
					continue
				}

				if err := login.Wait(t, waitFor); err != nil {
					atomic.AddInt64(&totalFailures, 1)
				} else {
					atomic.AddInt64(&totalSuccesses, 1)
				}
			}
		}(worker)
	}

	wg.Wait()

	t.Logf("Soak test completed after %v at concurrency %d:", duration, concurrency)
	t.Logf("  Total requests: %d", totalRequests)
	t.Logf("  Successes: %d, Failures: %d", totalSuccesses, totalFailures)
	if totalRequests > 0 {
		t.Logf("  Success rate: %.2f%%", 100.0*float64(totalSuccesses)/float64(totalRequests))
	}

	// Verify success rate is acceptable.
	if totalSuccesses == 0 {
		t.Fatal("no successful requests in soak test")
	}

	successRate := float64(totalSuccesses) / float64(totalRequests)
	if successRate < 0.95 { // At least 95% success rate
		t.Errorf("success rate too low: %.2f%%", 100.0*successRate)
	}

	// The point of a soak is what the server looks like afterward.
	assertServerStillServes(t, server.BaseURL)
}

// TestStress_BurstApprovals exercises the approval handler with sudden bursts
// of concurrent approvals (simulates a sudden rush of users). Tests queuing,
// backpressure, and recovery.
func TestStress_BurstApprovals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	idp := harness.NewIdentityProvider(t)
	server := harness.StartServer(t, idp, harness.ServerOptions{})
	agent := harness.StartAgent(t)
	_, ssoossh := harness.Binaries(t)

	// Start N concurrent logins in rapid succession, then approve all at once.
	n := 30
	var wg sync.WaitGroup
	var successCount int32

	// Start all logins without waiting.
	approvalURLs := make([]string, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// --force: these all share one agent, so a certificate issued
			// by an earlier burst member would otherwise be reused instead
			// of registering a new request.
			login := harness.StartLogin(t, ssoossh, server.BaseURL, agent.Socket, "--force")
			// Each goroutine owns its own slot, so no lock is needed.
			approvalURLs[idx] = login.ApprovalURL(t, waitFor)
		}(i)
	}

	wg.Wait()

	// Now approve all pending approvals in parallel.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			if approvalURLs[idx] == "" {
				return
			}
			if err := approveAs(server.BaseURL, approvalURLs[idx], fmt.Sprintf("user%d", idx)); err != nil {
				t.Logf("approving request %d failed: %v", idx, err)
				return
			}
			atomic.AddInt32(&successCount, 1)
		}(i)
	}

	wg.Wait()

	// Most approvals should succeed.
	if successCount < int32(n*9/10) { // At least 90%
		t.Errorf("approval success rate too low: %d/%d", successCount, n)
	}

	t.Logf("Burst approval test: %d/%d approvals succeeded", successCount, n)

	assertServerStillServes(t, server.BaseURL)
}
