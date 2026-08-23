# Load and Concurrency Tests

Load tests validate behavior under concurrent load, measuring performance degradation and looking for race conditions, goroutine leaks, or deadlocks.

## Running

```bash
# Run all load tests
go test -tags=load -count=1 -timeout=10m ./test/load/...

# Run with race detector
CGO_ENABLED=1 go test -tags=load -race -count=1 -timeout=10m ./test/load/...

# Run with -short flag (skips soak/long tests)
go test -tags=load -short -count=1 -timeout=5m ./test/load/...
```

## Test Categories

### Concurrent Operations (concurrent_test.go)
- **TestConcurrentLogins_10Simultaneous**: 10 parallel login+approval cycles
- **TestConcurrentLogins_50Simultaneous**: 50 parallel logins (heavy load)
- **TestSSEFanOut_100Subscribers**: 100 concurrent SSE stream subscribers
- **TestSerialNumberAllocation_Concurrent**: 20 concurrent certificate issuances with duplicate serial validation
- **TestApprovalRateLimiting_ConcurrentRequests**: Rate limit accuracy under concurrent load
- **TestCertificateSigningThroughput_HighLoad**: 30 concurrent signings with ops/sec measurement

### Soak and Stress Tests (soak_test.go)
- **TestSoak_SustainedLoad_1Hour**: Continuous load for 1 hour with goroutine/memory leak detection
- **TestSoak_SustainedLoad_30Minutes**: 30-minute soak test for CI
- **TestStress_BurstApprovals**: 30 simultaneous approvals (simulates traffic spike)
- **TestIdleness_LongLivedSSEStream**: Holds SSE stream open for extended period

## Assertions

Each test verifies:
1. **Success rate**: At least 95% of operations succeed
2. **Goroutine cleanup**: No more than ~10-20 goroutine leaks after cleanup
3. **Memory stability**: Memory growth <200 MB over baseline
4. **Throughput**: Operations complete within expected latency

## Metrics Reported

- Total requests, successes, and failures
- Success rate percentage
- Baseline, peak, and final goroutine counts
- Memory usage delta (baseline, max, final)
- Certificate signing throughput (ops/sec)
- Per-operation latency and variance
