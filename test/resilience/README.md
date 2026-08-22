# Resilience and Failure Injection Tests

Resilience tests validate that the ssoossh system handles infrastructure failures gracefully, without panicking, corrupting state, or hanging indefinitely. They run behind the `resilience` build tag.

## Running

```bash
# Run all resilience tests
go test -tags=resilience -count=1 ./test/resilience/...

# Run with race detector
CGO_ENABLED=1 go test -tags=resilience -race -count=1 ./test/resilience/...
```

## Test Categories

### Database Failures
- Connection pool exhaustion
- Mid-request disconnection
- Slow queries hitting timeouts
- Pool recovery on reconnect

### Identity Provider (OIDC) Failures
- Unreachable at startup (server starts degraded or refuses)
- Goes down mid-session
- JWKS endpoint unreachable at validation time
- JWKS key rotation mid-session
- Token endpoint returns 5xx, timeout, malformed JSON, or invalid response
- TLS failures

### Pub/Sub Failures
- Broker unavailable at startup
- Broker unavailable mid-run
- Messages dropped between approval and SSE delivery
- Subscriber that never drains (backpressure handling)

### SSE Client Failures
- Client vanishes without closing (half-open TCP)
- Thousands of idle streams held open (resource limits)
- Slow-reading client (one byte per second)
- Server shutdown with open streams (goroutine cleanup)

### Filesystem Failures
- Config file unreadable
- Disk full on log write
- SQLite file deleted/truncated/made read-only
- CA key file missing or permission denied

### Clock Failures
- System time jumping forward/backward
- Certificates issued at time boundaries

### Graceful Shutdown
- SIGTERM with in-flight requests
- SIGTERM with open SSE streams
- SIGTERM during signing operations

### Resource Limits
- File descriptor exhaustion
- Memory pressure on large request bodies
- Request body size limits enforcement

## Assertions

Every scenario validates three things:
1. **No panic or corruption**: The system doesn't crash or corrupt its state
2. **Clear error reporting**: A specific, actionable error is returned where possible
3. **Recovery**: When the dependency returns to health, the system resumes operation without restart

A hang (process not responding, goroutine leaks) is a failure.
