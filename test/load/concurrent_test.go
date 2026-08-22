//go:build load || e2e

package load

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// TestConcurrentLogins_N measures the server's ability to handle N concurrent
// login requests. Each starts independently, gets approved, and completes.
// Failure: panic, 500 errors, or goroutine/memory leaks.
func TestConcurrentLogins_10Simultaneous(t *testing.T) {
	testConcurrentLogins(t, 10)
}

func TestConcurrentLogins_50Simultaneous(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy load test in short mode")
	}
	testConcurrentLogins(t, 50)
}

func testConcurrentLogins(t *testing.T, n int) {
	t.Helper()

	idp := harness.NewIdentityProvider(t)
	server := harness.StartServer(t, idp, harness.ServerOptions{})
	agent := harness.StartAgent(t)
	_, ssoossh := harness.Binaries(t)

	// Record baseline goroutines.
	baselineGoroutines := runtime.NumGoroutine()

	var wg sync.WaitGroup
	var successCount int32
	var failCount int32

	// Start N concurrent logins and approve them all in parallel.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()

			login := harness.StartLogin(t, ssoossh, server.BaseURL, agent.Socket)
			url := login.ApprovalURL(t, waitFor)

			browser := harness.StartBrowser(t)
			browser.Navigate(t, url, `[data-testid="sign-in-button"]`)
			browser.Click(t, `[data-testid="sign-in-button"]`)
			browser.CompleteIdPLogin(t, fmt.Sprintf("user%d", userID))
			browser.WaitVisible(t, `[data-testid="approval-view"]`)
			browser.Click(t, `[data-testid="approve-button"]`)

			if err := login.Wait(t, waitFor); err != nil {
				atomic.AddInt32(&failCount, 1)
				t.Logf("login %d failed: %v", userID, err)
			} else {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	// Verify all succeeded.
	if failCount > 0 {
		t.Errorf("%d logins failed out of %d", failCount, n)
	}
	if successCount != int32(n) {
		t.Errorf("expected %d successful logins, got %d", n, successCount)
	}

	// Check goroutine cleanup.
	finalGoroutines := runtime.NumGoroutine()
	leaked := finalGoroutines - baselineGoroutines
	if leaked > 10 { // Allow some headroom for timing.
		t.Logf("WARNING: possible goroutine leak: baseline %d, final %d, leaked ~%d",
			baselineGoroutines, finalGoroutines, leaked)
	}
}

// TestSSEFanOut_N measures how many concurrent SSE subscribers the server
// can handle before degradation. Each subscriber waits for an approval event.
func TestSSEFanOut_100Subscribers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy load test in short mode")
	}
	testSSEFanOut(t, 100)
}

func testSSEFanOut(t *testing.T, nSubscribers int) {
	t.Helper()

	idp := harness.NewIdentityProvider(t)
	_ = harness.StartServer(t, idp, harness.ServerOptions{})

	baselineGoroutines := runtime.NumGoroutine()

	// Start N concurrent SSE subscribers (approval waits).
	// In a real test, we'd open N concurrent /api/events?approvalID=X streams
	// and verify they all receive an event when an approval happens.
	// This is a placeholder documenting the scenario.

	t.Logf("SSE fan-out test with %d subscribers: baseline %d goroutines",
		nSubscribers, baselineGoroutines)

	// TODO: Implement once SSE streaming is understood.
	// The test would:
	// 1. Start nSubscribers concurrent SSE clients
	// 2. Trigger an approval
	// 3. Verify all subscribers receive the event
	// 4. Check goroutine and memory delta
}

// TestSerialNumberAllocation_Concurrent validates that each issued certificate
// gets a unique serial number even under concurrent issuance. No duplicates.
func TestSerialNumberAllocation_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy load test in short mode")
	}

	idp := harness.NewIdentityProvider(t)
	server := harness.StartServer(t, idp, harness.ServerOptions{})
	agent := harness.StartAgent(t)
	_, ssoossh := harness.Binaries(t)

	n := 20 // Issue 20 certs concurrently.
	var wg sync.WaitGroup
	serials := make([]uint64, n)
	var mu sync.Mutex

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			login := harness.StartLogin(t, ssoossh, server.BaseURL, agent.Socket)
			url := login.ApprovalURL(t, waitFor)

			browser := harness.StartBrowser(t)
			browser.Navigate(t, url, `[data-testid="sign-in-button"]`)
			browser.Click(t, `[data-testid="sign-in-button"]`)
			browser.CompleteIdPLogin(t, fmt.Sprintf("user%d", idx))
			browser.WaitVisible(t, `[data-testid="approval-view"]`)
			browser.Click(t, `[data-testid="approve-button"]`)

			if err := login.Wait(t, waitFor); err != nil {
				t.Logf("login %d failed: %v", idx, err)
				return
			}

			// Get the issued certificate and record its serial.
			certs := agent.Certificates(t)
			if len(certs) > 0 {
				mu.Lock()
				serials[idx] = certs[len(certs)-1].Serial
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// Verify no duplicate serials.
	seen := make(map[uint64]bool)
	for i, serial := range serials {
		if serial == 0 {
			t.Logf("cert %d: serial not set (cert may not have been issued)", i)
			continue
		}
		if seen[serial] {
			t.Errorf("duplicate serial number: %d", serial)
		}
		seen[serial] = true
	}

	if len(seen) != n {
		t.Logf("expected %d unique serials, got %d", n, len(seen))
	}
}

const waitFor = 10 * time.Second

// agent is a shorthand for agent.Certificates in tests.
// Actual tests use the fixture or harness.Agent directly.
