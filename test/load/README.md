# Load and Concurrency Tests

Load tests validate behavior under concurrent load, measuring performance degradation and looking for race conditions, goroutine leaks, or deadlocks.

## Running

```bash
# Run all load tests
go test -tags=load -count=1 -timeout=5m ./test/load/...

# Run with race detector
CGO_ENABLED=1 go test -tags=load -race -count=1 -timeout=5m ./test/load/...
```

## Test Categories

- **Concurrent approvals**: Many clients requesting simultaneously
- **SSE fan-out**: Multiple concurrent SSE stream subscribers
- **Rate limiting under load**: Verify rate limits hold under parallel requests
- **Serial number allocation**: No duplicates under concurrent issuance
- **Session store concurrency**: Concurrent login/logout operations
- **Soak tests**: Sustained load over time, watching for memory/goroutine growth

## Metrics Reported

- Maximum concurrent streams before degradation
- Memory/goroutine delta before and after
- Rate limit accuracy (leaked requests vs enforced)
- Signing throughput (ops/sec under concurrency)
