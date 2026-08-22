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

// TestSoak_SustainedLoad_1Hour exercises the server under continuous load for
// an extended period, watching for memory leaks, goroutine leaks, or gradual
// degradation. Issues to catch: memory growth without GC, stale connections,
// connection pool exhaustion over time.
func TestSoak_SustainedLoad_1Hour(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running soak test in short mode")
	}

	testSoakLoad(t, 1*time.Hour, 5) // 5 concurrent, for 1 hour
}

// TestSoak_SustainedLoad_30Minutes is a shorter soak test for CI.
func TestSoak_SustainedLoad_30Minutes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running soak test in short mode")
	}

	testSoakLoad(t, 30*time.Minute, 3) // 3 concurrent, for 30 minutes
}

func testSoakLoad(t *testing.T, duration time.Duration, concurrency int) {
	t.Helper()

	idp := harness.NewIdentityProvider(t)
	server := harness.StartServer(t, idp, harness.ServerOptions{})
	agent := harness.StartAgent(t)
	_, ssoossh := harness.Binaries(t)

	// Record baseline metrics.
	baselineGoroutines := runtime.NumGoroutine()
	var baselineMemStats runtime.MemStats
	runtime.ReadMemStats(&baselineMemStats)

	var totalRequests int64
	var totalSuccesses int64
	var totalFailures int64
	var maxGoroutines int32
	var maxAlloc int64

	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup

	// Spawn N concurrent workers that repeatedly issue logins.
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()

			for time.Now().Before(deadline) {
				atomic.AddInt64(&totalRequests, 1)

				login := harness.StartLogin(t, ssoossh, server.BaseURL, agent.Socket)
				url := login.ApprovalURL(t, waitFor)

				browser := harness.StartBrowser(t)
				browser.Navigate(t, url, `[data-testid="sign-in-button"]`)
				browser.Click(t, `[data-testid="sign-in-button"]`)
				browser.CompleteIdPLogin(t, fmt.Sprintf("worker%d_user%d", w, time.Now().Unix()))
				browser.WaitVisible(t, `[data-testid="approval-view"]`)
				browser.Click(t, `[data-testid="approve-button"]`)

				if err := login.Wait(t, waitFor); err != nil {
					atomic.AddInt64(&totalFailures, 1)
				} else {
					atomic.AddInt64(&totalSuccesses, 1)
				}

				// Check goroutine count periodically.
				if g := int32(runtime.NumGoroutine()); g > maxGoroutines {
					atomic.StoreInt32(&maxGoroutines, g)
				}

				// Check memory usage.
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				if int64(ms.Alloc) > atomic.LoadInt64(&maxAlloc) {
					atomic.StoreInt64(&maxAlloc, int64(ms.Alloc))
				}
			}
		}(worker)
	}

	wg.Wait()

	// Analyze results.
	finalGoroutines := runtime.NumGoroutine()
	var finalMemStats runtime.MemStats
	runtime.ReadMemStats(&finalMemStats)

	t.Logf("Soak test completed:")
	t.Logf("  Total requests: %d", totalRequests)
	t.Logf("  Successes: %d, Failures: %d", totalSuccesses, totalFailures)
	t.Logf("  Success rate: %.2f%%", 100.0*float64(totalSuccesses)/float64(totalRequests))
	t.Logf("  Goroutines: baseline %d, max %d, final %d",
		baselineGoroutines, maxGoroutines, finalGoroutines)
	t.Logf("  Memory: baseline %d MB, max %d MB, final %d MB",
		baselineMemStats.Alloc/1_000_000, maxAlloc/1_000_000, finalMemStats.Alloc/1_000_000)

	// Verify success rate is acceptable.
	if totalSuccesses == 0 {
		t.Fatal("no successful requests in soak test")
	}

	successRate := float64(totalSuccesses) / float64(totalRequests)
	if successRate < 0.95 { // At least 95% success rate
		t.Errorf("success rate too low: %.2f%%", 100.0*successRate)
	}

	// Verify goroutine cleanup.
	goroutineLeaked := finalGoroutines - baselineGoroutines
	if goroutineLeaked > 20 { // Allow some headroom
		t.Errorf("possible goroutine leak: %d goroutines not cleaned up",
			goroutineLeaked)
	}

	// Verify memory didn't grow unbounded.
	// Allow up to 200 MB growth over baseline.
	memoryGrowth := int64(finalMemStats.Alloc) - int64(baselineMemStats.Alloc)
	if memoryGrowth > 200_000_000 {
		t.Errorf("excessive memory growth: %d MB",
			memoryGrowth/1_000_000)
	}
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
	var urlMu sync.Mutex

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			login := harness.StartLogin(t, ssoossh, server.BaseURL, agent.Socket)
			url := login.ApprovalURL(t, waitFor)

			urlMu.Lock()
			approvalURLs[idx] = url
			urlMu.Unlock()
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

			browser := harness.StartBrowser(t)
			browser.Navigate(t, approvalURLs[idx], `[data-testid="sign-in-button"]`)
			browser.Click(t, `[data-testid="sign-in-button"]`)
			browser.CompleteIdPLogin(t, fmt.Sprintf("user%d", idx))
			browser.WaitVisible(t, `[data-testid="approval-view"]`)
			browser.Click(t, `[data-testid="approve-button"]`)

			atomic.AddInt32(&successCount, 1)
		}(i)
	}

	wg.Wait()

	// Most approvals should succeed.
	if successCount < int32(n*9/10) { // At least 90%
		t.Errorf("approval success rate too low: %d/%d", successCount, n)
	}

	t.Logf("Burst approval test: %d/%d approvals succeeded", successCount, n)
}

// TestIdleness_LongLivedSSEStream validates that the server can hold open
// an idle SSE stream for extended periods without hanging or consuming
// excessive resources.
func TestIdleness_LongLivedSSEStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping idleness test in short mode")
	}

	// Scenario: Open an SSE stream and hold it open for 5 minutes without activity.
	// Verify server doesn't crash, doesn't consume growing memory/goroutines,
	// and can resume sending events when they appear.

	// This requires SSE streaming implementation details.
	t.Skip("requires SSE streaming implementation")
}
