# Test Suite Overview

This document describes the comprehensive test suites for ssoossh covering resilience, load, and accessibility scenarios.

## Build Tags and Organization

Test suites use Go build tags to separate concerns:

- **`-tags=load,e2e`**: Load and concurrency tests (stress testing, soak testing, throughput)
- **`-tags=resilience,e2e`**: Resilience tests (infrastructure failures, edge cases, graceful shutdown)
- **Frontend**: Vitest for Svelte components (TypeScript/accessibility tests)

All test files use `//go:build <tag> || e2e` to enable running via either the specific tag or the generic e2e tag.

## Load and Concurrency Tests (`test/load/`)

**Files:**
- `concurrent_test.go`: Concurrent operation tests
- `soak_test.go`: Sustained load and stress tests
- `README.md`: Documentation

### Test Coverage

**Concurrent Operations** (4 tests):
- `TestConcurrentLogins_10Simultaneous`, `TestConcurrentLogins_50Simultaneous` -- simultaneous logins with goroutine/memory leak detection
- `TestSerialNumberAllocation_Concurrent` -- concurrent certificate serial number allocations (no duplicates)
- `TestCertificateSigningThroughput_HighLoad` -- concurrent certificate signing with throughput measurement

**Soak and Stress** (3 tests):
- `TestSoak_SustainedLoad_ThreeWorkers`, `TestSoak_SustainedLoad_FiveWorkers` -- sustained load with a 95%+ success rate requirement
- `TestStress_BurstApprovals` -- burst of concurrent approvals (traffic spike simulation)

### Assertions

Each test validates:
1. **Success Rate**: ≥95% of operations complete successfully
2. **Resource Cleanup**: Goroutines leaked ≤10-20, memory growth <200 MB
3. **Throughput**: Operations/sec measured and logged
4. **Degradation**: No crashes, panics, or state corruption under load

### Running

```bash
# Quick test (just concurrent, skips soak)
go test -tags=load -short -count=1 -timeout=5m ./test/load/...

# Full suite (includes the soak tests)
go test -tags=load -count=1 -timeout=60m ./test/load/...

# With race detector
CGO_ENABLED=1 go test -tags=load -race -count=1 -timeout=60m ./test/load/...
```

## Resilience Tests (`test/resilience/`)

**Files:**
- `database_test.go`: Database failure scenarios
- `oidc_test.go`: Identity provider recovery
- `shutdown_test.go`: Graceful shutdown and recovery
- `resource_limits_test.go`: Edge cases
- `fixture.go`: Shared test infrastructure
- `README.md`: Documentation

### Test Coverage

Fourteen tests. The placeholder scenarios an earlier revision listed here
(OIDC failure injection, resource limits, rate limits, SSE shutdown) were
unconditional `t.Skip` calls and were deleted; see `TESTING_SUMMARY.md`.

**Database Failures** (6 tests):
- `TestDatabase_HealthzSucceedsWithSlowQueries` -- slow queries don't block healthz
- `TestDatabase_CertificateIssuanceSucceedsUnderLoad`
- `TestDatabase_RequestContextCancelledDoesNotCorruptState`
- `TestDatabase_MultipleParallelRequestsAreIsolated` (ACID)
- `TestDatabase_ServerShutdownBlocksGracefully` -- graceful shutdown closes connections
- `TestDatabase_CertificateSerialIsIncremented` -- serial numbers are unique and incremented

**OIDC Provider Recovery** (1 test):
- `TestOIDC_LoginSucceedsAfterIdPRecovery` -- recovery after IdP outage

**Graceful Shutdown** (4 tests):
- `TestShutdown_SIGTERMWithInFlightRequests`
- `TestShutdown_SIGTERMDuringCertificateSigning`
- `TestShutdown_GracefulWithDatabaseConnections` -- database connections cleanly closed
- `TestRecovery_AfterShutdown` -- state recovery after restart

**Edge Cases** (3 tests):
- `TestEdgeCase_DuplicateApprovalClick` (idempotency)
- `TestEdgeCase_CertificateWithExpiredToken` -- certificate validity independent of token expiry
- `TestEdgeCase_ConcurrentApprovalsOfSameLogin`

### Assertions

Each test validates:
1. **No Panic/Crash**: System handles failure gracefully
2. **Clear Error**: Actionable error message returned (not generic 500)
3. **Recovery**: System resumes normal operation when dependency recovers
4. **State Integrity**: No data corruption, orphaned rows, or inconsistency

### Running

```bash
# Quick tests (skips long-running scenarios)
go test -tags=resilience -short -count=1 -timeout=5m ./test/resilience/...

# Full suite
go test -tags=resilience -count=1 -timeout=10m ./test/resilience/...

# With race detector
CGO_ENABLED=1 go test -tags=resilience -race -count=1 -timeout=10m ./test/resilience/...
```

## Frontend Accessibility Tests (`frontend/src/lib/components/`)

**File:** `ConsentModal.a11y.test.ts`

### Test Coverage (16 tests)

- Automated a11y violation scanning (jest-axe)
- Dialog role and aria-modal attribute
- Accessible modal name
- Focus management on open
- Focus trapping within modal
- Escape key blocking (cannot dismiss)
- High contrast text and button colors
- Touch target sizing (≥44x44px)
- Screen reader text announcement
- Clear, descriptive button labels
- Keyboard navigation (Tab support)
- Button activation with Enter key
- Reduced motion preferences
- High contrast mode support
- Visible focus indicators
- Semantic HTML structure (use `<dialog>`)

### Assertions

Tests validate WCAG 2.1 Level AA compliance:
- No automated accessibility violations (axe)
- Proper ARIA roles and attributes
- Keyboard operability
- Color contrast requirements
- Touch target sizing
- Focus visibility
- Semantic markup

### Running

```bash
# Install dependencies
cd frontend
pnpm install

# Run a11y tests
pnpm test

# Watch mode
pnpm test:watch
```

## CI/CD Integration

`.github/workflows/resilience.yaml` defines four test jobs behind a
`changes` path-filter gate:

```yaml
resilience:
  # go test -tags=resilience -race -count=1 -timeout=5m ./test/resilience/...

load:
  # go test -tags=load -race -count=1 -timeout=5m ./test/load/...

migrations:
  # go test -tags=dbparity ./test/migration/...  (schema parity, needs docker)

a11y:
  # pnpm test (frontend accessibility)
```

## Metrics and Reporting

Each test logs key metrics:

**Load Tests:**
- Total requests, successes, failures
- Success rate %
- Goroutine delta (baseline → peak → final)
- Memory usage (baseline, peak, final)
- Ops/sec for throughput tests

**Resilience Tests:**
- Test outcome (pass/fail/skip)
- Error message and recovery confirmation
- State validation results

**A11y Tests:**
- Automated violation count (axe)
- Individual assertion pass/fail
- WCAG 2.1 level compliance

## Test Composition Strategy

### Shared Fixture Pattern

Resilience tests use a shared `fixture` type (`fixture.go`):
- Starts server, IdP, and agent once per test
- Provides `startBrowser()` for lazy browser initialization
- Cleanup via `t.Cleanup()` handlers

This minimizes startup/teardown overhead and enables realistic multi-step scenarios (login → approval → validation).

### Concurrent Test Pattern

Load tests use `sync.WaitGroup` and atomic counters:
- Spawn N goroutines, each running a full login+approval cycle
- Collect success/fail counts atomically (no race conditions)
- Record baseline/peak/final goroutines and memory
- Report metrics after all goroutines complete

### No Placeholders

A test that cannot run does not exist in these suites. An earlier revision
kept scenarios beyond the harness as unconditional `t.Skip("requires X")`
functions; all 26 were deleted (see `TESTING_SUMMARY.md`), because a skip
reports green for something nobody checked. The only skips left are
`testing.Short()` guards on the long-running tests. Scenarios the harness
cannot reach are listed under "Known Limitations" below, not in code.

## Adding New Tests

Follow these patterns:

1. **Resilience Test**: Use `newFixture(t)`, call harness methods, verify outcomes
2. **Load Test**: Use `sync.WaitGroup`, `sync.atomic` counters, measure baseline/peak/final metrics
3. **A11y Test**: Use `@testing-library/svelte`, `jest-axe`, and WCAG 2.1 assertions

All tests must:
- Have clear, descriptive names: "should [action] when [condition]"
- Include a doc comment explaining what they test
- Validate exactly what they claim to test (one assertion per test when possible)
- Log metrics or errors via `t.Logf()` for debugging

## Known Limitations

The harness cannot reach these scenarios, so nothing tests them:

- **SSE streaming**: No stream fan-out or event delivery testing
- **Pub/Sub broker**: No message broker failure injection
- **Filesystem failures**: No disk-full or permission-denied injection
- **Clock skew**: No system time manipulation
- **IdP failure injection**: Some OIDC scenarios require live IdP failure
- **OS resource limits**: FD exhaustion and memory pressure need OS-level injection

They are recorded here rather than as skipped tests.
