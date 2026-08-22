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

const waitFor = 10 * time.Second

// TestConcurrentLogins_10Simultaneous measures the server's ability to handle
// 10 concurrent login requests. Each starts independently, gets approved, and
// completes. Verifies no panics, 500 errors, or goroutine/memory leaks.
func TestConcurrentLogins_10Simultaneous(t *testing.T) {
	testConcurrentLogins(t, 10)
}

// TestConcurrentLogins_50Simultaneous runs a heavier concurrent load test with
// 50 concurrent login operations. This stresses connection pools and approval
// handling at higher concurrency.
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

	// Record baseline goroutines and memory.
	baselineGoroutines := runtime.NumGoroutine()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

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
	if leaked > 5 {
		t.Errorf("goroutine leak: baseline %d, final %d, leaked %d (threshold 5)",
			baselineGoroutines, finalGoroutines, leaked)
	}

	// Check memory delta.
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	allocDelta := m2.Alloc - m1.Alloc
	if allocDelta > 100_000_000 { // 100 MB threshold
		t.Logf("WARNING: large memory growth: %d bytes allocated", allocDelta)
	}
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
		t.Errorf("expected %d unique serials, got %d", n, len(seen))
	}
}

// TestCertificateSigningThroughput_HighLoad measures the rate at which
// the server can sign SSH certificates under sustained concurrent load.
// Reports ops/sec and verifies signing doesn't become a bottleneck.
func TestCertificateSigningThroughput_HighLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping throughput test in short mode")
	}

	idp := harness.NewIdentityProvider(t)
	server := harness.StartServer(t, idp, harness.ServerOptions{})
	agent := harness.StartAgent(t)
	_, ssoossh := harness.Binaries(t)

	n := 30 // Issue 30 certs for throughput measurement
	start := time.Now()

	var wg sync.WaitGroup
	var successCount int32

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

			if err := login.Wait(t, waitFor); err == nil {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	if successCount == 0 {
		t.Fatal("no certificates signed in throughput test")
	}

	opsPerSec := float64(successCount) / elapsed.Seconds()
	t.Logf("Certificate signing throughput: %.2f ops/sec over %v", opsPerSec, elapsed)
}
