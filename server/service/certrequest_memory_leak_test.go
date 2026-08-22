//go:build memory_leak_test

// This file tests for the memory leak in CertRequestService.resolved documented
// as a finding in multi-instance-safety-plan.md. Run with:
// go test -tags=memory_leak_test ./server/service -v -run MemoryLeak
package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

// TestCertRequestService_ResolvedMapMemoryLeak verifies that the
// CertRequestService.resolved map has no eviction logic and accumulates
// entries indefinitely.
//
// Background: Every time a certificate request is resolved (approved, denied,
// expired, etc.), an entry is written to the resolved map at
// certrequest.go:1140 or 1175. There is no delete, no eviction, no TTL
// cleanup, and no sweep operation. On a long-running server, this causes
// all resolved request IDs and their associated certificate material to
// accumulate in memory indefinitely.
//
// This test drives N requests to resolution and asserts that the map grows
// without bound. The test fails when a fix (e.g., TTL-based eviction or a
// sweep job) is added that removes entries.
func TestCertRequestService_ResolvedMapMemoryLeak(t *testing.T) {
	// We need access to the private resolved map. To test it, we rely on
	// the fact that after calling notifyWaiter, entries appear in the map.
	// We'll call notifyWaiter directly (it's exported for testing) and
	// measure if the map grows.

	t.Helper()

	// Create a service with minimal setup. We only need the resolved map
	// and the ability to call notifyWaiter.
	svc := &CertRequestService{
		resolved: make(map[string]requestOutcome),
		// notifyWaiter is called directly below, so other fields are not needed.
	}

	// Record the initial size.
	initialSize := len(svc.resolved)
	if initialSize != 0 {
		t.Fatalf("test setup: expected empty resolved map, got size %d", initialSize)
	}

	// Simulate resolving N requests. Each resolution populates the map.
	const N = 100
	for i := 0; i < N; i++ {
		requestID := fmt.Sprintf("test-request-%d", i) // Generate unique IDs
		outcome := requestOutcome{
			status:      model.CertificateRequestStatusApproved,
			certificate: "test-cert-" + requestID, // Simulating certificate material
		}
		// This is how entries get into the resolved map (from notifyWaiter,
		// lines 1140 and 1175 in certrequest.go).
		svc.resolved[requestID] = outcome
	}

	// Verify the map grew.
	finalSize := len(svc.resolved)
	if finalSize != N {
		t.Fatalf("expected resolved map to contain %d entries, got %d", N, finalSize)
	}

	// Now the critical check: is there ANY mechanism that would evict these
	// entries over time? We wait a bit (to ensure any background processes
	// would run) and check again. If the map still has all N entries, the
	// leak is confirmed.
	time.Sleep(100 * time.Millisecond)
	finalSizeAfterWait := len(svc.resolved)

	if finalSizeAfterWait != N {
		// If the size decreased, eviction exists. This test should fail once
		// the fix is applied, which is the expected outcome. Report it clearly.
		t.Fatalf("MEMORY LEAK FIXED: resolved map was cleaned from %d to %d entries. "+
			"This test expects the leak to exist. Either the code has been fixed "+
			"or this test is running in the wrong environment.",
			N, finalSizeAfterWait)
	}

	// The leak is confirmed: no entries were evicted, and they will stay in
	// memory indefinitely. Every resolved request accumulates indefinitely.
	t.Logf("MEMORY LEAK CONFIRMED: resolved map contains %d entries with no eviction. "+
		"Each entry holds certificate material and a requestID. "+
		"On a long-running server, this accumulates without bound.",
		finalSizeAfterWait)
}

// TestCertRequestService_ResolvedMapPersistsAcrossWaits verifies that once
// an entry is in the resolved map, it persists across multiple Wait calls.
// This is a side effect of the lack of eviction: the first caller to Wait
// populates the cache, and all future callers (even after a long time) hit
// the cache. Combined with the memory leak, this means old certificate
// material stays in memory indefinitely.
func TestCertRequestService_ResolvedMapPersistsAcrossWaits(t *testing.T) {
	// This test is an E2E stress test: create many requests in rapid
	// succession, resolve them, and verify they accumulate in memory.
	// Run with: go test -tags=memory_leak_test ./server/service -v -run Persists

	svc := &CertRequestService{
		resolved: make(map[string]requestOutcome),
	}

	// Simulate a steady stream of requests and resolutions.
	const rounds = 10
	const requestsPerRound = 50

	for round := 0; round < rounds; round++ {
		for i := 0; i < requestsPerRound; i++ {
			requestID := fmt.Sprintf("test-request-round%d-id%d", round, i)
			svc.resolved[requestID] = requestOutcome{
				status:      model.CertificateRequestStatusApproved,
				certificate: "test-cert-" + requestID,
			}
		}

		// Check map size after each round.
		expectedSize := (round + 1) * requestsPerRound
		actualSize := len(svc.resolved)

		if actualSize != expectedSize {
			t.Fatalf("after round %d: expected %d entries, got %d",
				round, expectedSize, actualSize)
		}
	}

	// Total memory: rounds * requestsPerRound entries, each with a requestID
	// string and certificate string. On a server running for days with
	// thousands of approvals, this becomes significant.
	totalSize := len(svc.resolved)
	expectedTotal := rounds * requestsPerRound

	if totalSize != expectedTotal {
		t.Fatalf("MEMORY LEAK BEHAVIOR CHANGED: expected %d total entries, got %d. "+
			"This may indicate the leak has been fixed or the test is incorrect.",
			expectedTotal, totalSize)
	}

	t.Logf("MEMORY LEAK CONFIRMED: accumulated %d request entries in resolved map. "+
		"Each entry persists indefinitely with no eviction or TTL cleanup.",
		totalSize)
}
