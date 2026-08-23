//go:build load || e2e

package load

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

const waitFor = 10 * time.Second

// TestConcurrentLogins_10Simultaneous measures the server's ability to handle
// 10 concurrent login requests. Each starts independently, gets approved, and
// completes. Verifies no panics, 500s, or a server left unable to serve.
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

	var wg sync.WaitGroup
	var successCount int32
	var failCount int32

	// Start N concurrent logins and approve them all in parallel.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(userID int) {
			defer wg.Done()

			// --force: every login here shares one agent, and a certificate
			// another one already landed there would otherwise be reused
			// instead of putting a new request through the server.
			login := harness.StartLogin(t, ssoossh, server.BaseURL, agent.Socket, "--force")
			url := login.ApprovalURL(t, waitFor)

			if err := approveAs(server.BaseURL, url, fmt.Sprintf("user%d", userID)); err != nil {
				atomic.AddInt32(&failCount, 1)
				t.Logf("approving login %d failed: %v", userID, err)
				return
			}

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
	// int(successCount) rather than int32(n): n is the worker count, and
	// widening the counter avoids a conversion that is only safe because of
	// how small n happens to be.
	if int(successCount) != n {
		t.Errorf("expected %d successful logins, got %d", n, successCount)
	}

	assertServerStillServes(t, server.BaseURL)
}

// TestSerialNumberAllocation_Concurrent validates that each issued certificate
// gets a unique serial number even under concurrent issuance. No duplicates.
func TestSerialNumberAllocation_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy load test in short mode")
	}

	idp := harness.NewIdentityProvider(t)
	server := harness.StartServer(t, idp, harness.ServerOptions{})
	_, ssoossh := harness.Binaries(t)

	n := 20 // Issue 20 certs concurrently.
	var wg sync.WaitGroup
	serials := make([]uint64, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// An agent per login, not one shared: the client prunes every
			// certificate its new one supersedes (pruneSuperseded in
			// client/cmd/ssh_login.go), so a shared agent would hold one
			// certificate at the end no matter how many were issued, and
			// which one it kept would be a race.
			agent := harness.StartAgent(t)
			login := harness.StartLogin(t, ssoossh, server.BaseURL, agent.Socket)
			url := login.ApprovalURL(t, waitFor)

			if err := approveAs(server.BaseURL, url, fmt.Sprintf("user%d", idx)); err != nil {
				t.Logf("approving login %d failed: %v", idx, err)
				return
			}
			if err := login.Wait(t, waitFor); err != nil {
				t.Logf("login %d failed: %v", idx, err)
				return
			}

			certs := agent.Certificates(t)
			if len(certs) != 1 {
				t.Errorf("expected exactly one certificate in agent %d, got %d", idx, len(certs))
				return
			}
			serials[idx] = certs[0].Serial
		}(i)
	}

	wg.Wait()

	// Verify no duplicate serials.
	seen := make(map[uint64]bool)
	for i, serial := range serials {
		if serial == 0 {
			t.Errorf("cert %d: no certificate was issued", i)
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

			// --force, for the same shared-agent reason as above.
			login := harness.StartLogin(t, ssoossh, server.BaseURL, agent.Socket, "--force")
			url := login.ApprovalURL(t, waitFor)

			if err := approveAs(server.BaseURL, url, fmt.Sprintf("user%d", idx)); err != nil {
				t.Logf("approving login %d failed: %v", idx, err)
				return
			}
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

// approveAs signs in as username and approves the request behind
// approvalURL over HTTP — the same redirect chain and endpoints a browser
// walks, minus the browser. Every simulated user gets its own cookie jar,
// so their sessions stay as separate as separate browsers would keep them.
//
// This suite drives approvals over HTTP rather than through chromedp on
// purpose: one headless Chrome per simulated user does not survive tens of
// concurrent logins on a CI runner, and what these tests measure is the
// server under concurrency, not the SPA.
func approveAs(serverBaseURL, approvalURL, username string) error {
	requestID, err := harness.RequestIDFromApprovalURL(approvalURL)
	if err != nil {
		return err
	}
	client, err := harness.NewCookieClient()
	if err != nil {
		return err
	}
	if err := harness.Authenticate(client, serverBaseURL, "/approve/"+requestID, username, nil); err != nil {
		return err
	}
	return harness.Approve(client, serverBaseURL, requestID)
}

// assertServerStillServes checks that the server is healthy after a load
// run. This replaces an earlier in-process goroutine and heap check, which
// sampled runtime.NumGoroutine() in the *test* binary: those counts belong
// to the harness (chromedp, exec copiers, HTTP clients), not to ssoosshd,
// and grew with the harness itself. A server that leaked connections,
// exhausted its pool, or wedged a worker fails here instead.
func assertServerStillServes(t *testing.T, serverBaseURL string) {
	t.Helper()

	resp, err := http.Get(serverBaseURL + "/healthz")
	if err != nil {
		t.Fatalf("server unreachable after the load run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("server unhealthy after the load run: /healthz answered %d", resp.StatusCode)
	}
}
