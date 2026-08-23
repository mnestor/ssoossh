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

### Database Failures (database_test.go)
- **TestDatabase_HealthzSucceedsWithSlowQueries**: Healthz responsive despite slow queries
- **TestDatabase_CertificateIssuanceSucceedsUnderLoad**: Signing works under moderate database load
- **TestDatabase_RequestContextCancelledDoesNotCorruptState**: Cancelled requests don't corrupt state
- **TestDatabase_MultipleParallelRequestsAreIsolated**: Concurrent requests see isolated data
- **TestDatabase_ServerShutdownBlocksGracefully**: Shutdown cleans up database connections
- **TestDatabase_CertificateSerialIsIncremented**: Each cert gets unique serial number

### Identity Provider (OIDC) Failures (oidc_test.go)
- **TestOIDC_TokenEndpointTimeout**: Timeout handling without panic
- **TestOIDC_JWKSEndpointUnreachable**: JWKS fetch failure returns 401/403, not 500
- **TestOIDC_KeyRotationMidSession**: Smooth transition when IdP rotates signing keys
- **TestOIDC_TokenEndpointMalformedResponse**: Invalid JSON doesn't panic
- **TestOIDC_TokenEndpointReturns5xx**: 5xx errors don't cause retry loops
- **TestOIDC_TLSHandshakeFails**: TLS errors are handled cleanly
- **TestOIDC_LoginSucceedsAfterIdPRecovery**: Logins work after IdP outage resolves

### Graceful Shutdown (shutdown_test.go)
- **TestShutdown_SIGTERMWithInFlightRequests**: SIGTERM handles in-flight operations
- **TestShutdown_SIGTERMWithOpenSSEStreams**: SSE streams close cleanly on shutdown
- **TestShutdown_SIGTERMDuringCertificateSigning**: Signing operations either complete or abort cleanly
- **TestShutdown_GracefulWithDatabaseConnections**: Database connections closed without leaks
- **TestShutdown_TimeoutPreventingHang**: Timeout mechanism prevents indefinite hangs
- **TestRecovery_AfterShutdown**: State recovered from database after restart

### Resource Limits and Edge Cases (resource_limits_test.go)
- **TestResourceLimits_RequestBodySize**: Oversized requests rejected without panic
- **TestResourceLimits_TooManyHeaders**: Many headers parsed without hang
- **TestResourceLimits_FileDescriptorExhaustion**: FD exhaustion handled gracefully
- **TestResourceLimits_MemoryPressure**: Allocation failures don't panic
- **TestRateLimit_ApprovalsPerUser**: Per-user rate limits enforced cleanly
- **TestRateLimit_LoginsPerIP**: Per-IP rate limits enforced cleanly
- **TestEdgeCase_EmptyUserIDInApproval**: Malformed requests rejected with 400-level error
- **TestEdgeCase_DuplicateApprovalClick**: Second approval is no-op or rejected
- **TestEdgeCase_ApprovalBeforeLogin**: Unauthenticated approval fails cleanly
- **TestEdgeCase_CertificateWithExpiredToken**: Certificate validity independent of token
- **TestEdgeCase_ConcurrentApprovalsOfSameLogin**: Only one cert issued for concurrent approvals
- **TestEdgeCase_LoginTimeoutNeverReceivesApproval**: Expired login cleaned up properly

### Not Yet Implemented
- **Pub/Sub Failures**: Broker unavailable at startup/mid-run, message delivery
- **SSE Client Failures**: Half-open TCP, resource limits, slow readers, cleanup
- **Filesystem Failures**: Config/SQLite/CA key access issues
- **Clock Failures**: System time jumps, boundary conditions

## Assertions

Every scenario validates three things:
1. **No panic or corruption**: The system doesn't crash or corrupt its state
2. **Clear error reporting**: A specific, actionable error is returned where possible
3. **Recovery**: When the dependency returns to health, the system resumes operation without restart

A hang (process not responding, goroutine leaks) is a failure.
