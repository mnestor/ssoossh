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
- `concurrent_test.go` (7.9 KB): Concurrent operation tests
- `soak_test.go` (6.5 KB): Sustained load and stress tests
- `README.md` (3.9 KB): Documentation

### Test Coverage

**Concurrent Operations** (6 tests):
- 10 and 50 simultaneous logins with goroutine/memory leak detection
- 100 concurrent SSE subscribers
- 20 concurrent certificate serial number allocations (no duplicates)
- Concurrent approval rate limiting accuracy
- 30 concurrent certificate signing with throughput measurement

**Soak and Stress** (4 tests):
- 1-hour and 30-minute sustained load with 95%+ success rate requirement
- Burst of 30 concurrent approvals (traffic spike simulation)
- Long-lived SSE streams (placeholder for implementation)

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

# Full suite (includes 30+ minute soak)
go test -tags=load -count=1 -timeout=60m ./test/load/...

# With race detector
CGO_ENABLED=1 go test -tags=load -race -count=1 -timeout=60m ./test/load/...
```

## Resilience Tests (`test/resilience/`)

**Files:**
- `database_test.go` (8.2 KB): Database failure scenarios
- `oidc_test.go` (4.2 KB): Identity provider failure injection
- `shutdown_test.go` (5.4 KB): Graceful shutdown and recovery
- `resource_limits_test.go` (7.8 KB): Resource limits and edge cases
- `fixture.go` (2.0 KB): Shared test infrastructure
- `README.md` (3.9 KB): Documentation

### Test Coverage

**Database Failures** (6 tests):
- Slow queries don't block healthz
- Certificate issuance succeeds under load
- Context cancellation doesn't corrupt state
- Parallel requests are isolated (ACID)
- Graceful shutdown closes connections
- Serial numbers are unique and incremented

**OIDC Provider Failures** (7 tests):
- Token endpoint timeout handling
- JWKS endpoint unreachability
- OIDC key rotation mid-session
- Malformed token responses
- 5xx errors from token endpoint
- TLS handshake failures
- Recovery after IdP outage

**Graceful Shutdown** (6 tests):
- SIGTERM with in-flight requests
- SIGTERM with open SSE streams (placeholder)
- SIGTERM during certificate signing
- Database connections cleanly closed
- Timeout mechanism prevents hang
- State recovery after restart

**Resource Limits and Edge Cases** (12 tests):
- Request body size limits
- Header count limits (placeholder)
- File descriptor exhaustion (placeholder)
- Memory pressure handling (placeholder)
- Per-user approval rate limits (placeholder)
- Per-IP login rate limits (placeholder)
- Empty/malformed approval requests
- Duplicate approval clicks (idempotency)
- Approval without prior login (placeholder)
- Certificate validity independent of token expiry
- Concurrent approvals of same login
- Login timeout expiry and cleanup

### Assertions

Each test validates:
1. **No Panic/Crash**: System handles failure gracefully
2. **Clear Error**: Actionable error message returned (not generic 500)
3. **Recovery**: System resumes normal operation when dependency recovers
4. **State Integrity**: No data corruption, orphaned rows, or inconsistency

### Running

```bash
# Quick tests (skips placeholders and long-running scenarios)
go test -tags=resilience -short -count=1 -timeout=5m ./test/resilience/...

# Full suite
go test -tags=resilience -count=1 -timeout=10m ./test/resilience/...

# With race detector
CGO_ENABLED=1 go test -tags=resilience -race -count=1 -timeout=10m ./test/resilience/...
```

## Frontend Accessibility Tests (`frontend/src/lib/components/`)

**File:** `ConsentModal.a11y.test.ts` (2.8 KB)

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

`.github/workflows/resilience.yaml` defines three jobs:

```yaml
resilience:
  # go test -tags=resilience -race -count=1 -timeout=5m ./test/resilience/...

load:
  # go test -tags=load -race -count=1 -timeout=5m ./test/load/...

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

### Placeholder and Skip Pattern

Tests document scenarios that require infrastructure beyond the current harness:

```go
func TestOIDC_TokenEndpointTimeout(t *testing.T) {
	t.Skip("requires IdP failure injection capability")
	// Comment explains what the test will verify when implemented
}
```

This keeps the full test suite visible while being honest about what can be tested today.

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

Tests currently placeholder these scenarios (require harness enhancements):

- **SSE streaming**: No stream fan-out or event delivery testing
- **Pub/Sub broker**: No message broker failure injection
- **Filesystem failures**: No disk-full or permission-denied injection
- **Clock skew**: No system time manipulation
- **IdP failure injection**: Some OIDC scenarios require live IdP failure
- **OS resource limits**: FD exhaustion and memory pressure need OS-level injection

These are documented in test comments as `t.Skip()` calls.
