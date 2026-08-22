# Test Coverage Gap Map

**Measured**: 2026-08-22 (re-measured on current main after rebase)
**Overall coverage**: 88.9% (1406 tests passing)
**Baseline**: Run `go test -cover ./...` on main branch

## Summary

Coverage varies by package, from 100% (well-tested packages) to 0% (no tests). This map identifies specific untested functions and paths that will be addressed in Phase 1-4.

**Current status:**
- E2E tests: 11 tests passing in ~38s (tier-1 wire, tier-2 browser, tier-3 ssh)
- Unit tests: 1406 passing across 36 packages
- Zero-coverage packages: 8 (cmd/ssoossh, cmd/ssoosshd, server/certmsg, server/model, server/testutil, internal/apitypes)

### Packages at 0% Coverage (No Tests)

These packages have no test coverage at all and need comprehensive test suites:

| Package | Functions | Issue | Priority | Notes |
| --- | --- | --- | --- | --- |
| `cmd/ssoossh` | 1 | main entry point | P2 | Extract testable logic per go.md; main() itself may be excluded if it's a 3-line wiring shim. |
| `cmd/ssoosshd` | 1 | main entry point | P2 | Extract testable logic per go.md; main() itself may be excluded if it's a 3-line wiring shim. |
| `internal/apitypes` | 1 | Type definitions | P2 | `TerminalStatuses()` needs test + integration coverage of status lifecycle. |
| `server/certmsg` | 2 | Certificate message channel | P1 | `WaitTopic()`, `Failed()` - critical path for approval flow; drives SSE delivery. |
| `server/model` | 7 | Data models | P1 | ORM models for users, certificates, requests, enrollments, approvals. |
| `server/testutil` | 1 | Test utilities | Skip | Utilities for other tests, not production code. |

### Packages Under 90% Coverage (Partial Tests)

| Package | Coverage | Functions | Key Gaps |
| --- | --- | --- | --- |
| `client/cmd` | 88.7% | 74 | Command execution, API client initialization, browser opening, certificate type display. |
| `client/config` | 92.9% | 22 | Home directory lookup, plist dictionary parsing, config merging. |
| `internal/api` | 86.6% | 17 | Error stringer; SSE connection error paths; request creation edge cases. |
| `internal/serial` | 80.0% | 1 | Single function needs testing. |
| `internal/tools` | 62.2% | 3 | Build tool utilities. |
| `server/bootstrap` | 92.5% | 50 | DB pool config, migrations, logging init, router setup, scheduler registration. |
| `server/cmd` | 88.3% | 21 | Command construction and execution. |
| `server/config` | 54.8% | 27 | Config type methods (FIPSEnabled, IsAdminEnabled, IsAuditorEnabled, IsSSHServerAdminEnabled), logging config, HTTP config parsing. |
| `server/controller` | 81.6% | 57 | **CRITICAL**: Admin routes (0% - admin controller, handlers); auth paths (loginHandler 77.8%, callbackHandler 74.2%); approval UI (responses, certificates list); enrollment. |
| `server/dbtime` | 85.6% | 6 | Time normalization for databases. |
| `server/middleware` | 95.5% | 69 | OIDC verifier setup/teardown (0%), error code mapping (22.2%). |
| `server/pubsub` | 86.4% | 4 | Pub/sub connection/subscription failures. |
| `server/service` | 90.8% | 68 | Startup config validation (0%), decision creation (70%), wake messages (71.4%), request approval paths, lifetime policies, SSH admin checks. |
| `server/signer` | 98.6% | 13 | Certificate recording edge cases. |
| `server/utils` | 73.9% | 23 | Error response type methods (ErrorCode methods 0%); logging utilities. |

### Packages Near 100% (Minor Gaps)

| Package | Coverage | Notes |
| --- | --- | --- |
| `internal/crypto/ssh/agent` | 97.0% | `Certificates()` method 94.7% - error path in listing |
| `internal/crypto/ssh/keypair` | 93.3% | Several functions 100%, but `VerifyCertSignature()` 88.9%, `MarshalPrivateKey()` 85.7%, keypair generation exclusions |
| `server/config/tlsutils` | 98.9% | `Build()` 95.8% - TLS config edge cases |
| `server/frontend` | 97.0% | Frontend file embedding edge cases |
| `server/job` | 93.5% | Job execution paths |
| `server/pubsub` | 87.5% | Pub/sub connection/subscription failures |

### 100% Coverage (Well Tested)

- `internal/crypto/ssh` — principal validation
- `internal/errs` — error handling
- `internal/fipsmode` — FIPS mode checking
- `internal/principalsmap` — principals file loading
- `server/logging` — structured logging
- `server/utils/tracing` — trace ID generation
- All certificate/key generation algorithms (RSA, ECDSA, Ed25519)

## Frontend (Phase 3)

Coverage is currently unmeasurable (no provider configured).

**Known untested components** (from visual scan, verified against final coverage report):
- `Alert`, `Button`, `Card` — basic UI primitives
- `DetailRow`, `Icon`, `MonoChip` — detail display
- `OptionDiffList`, `SectionLabel`, `StatusBadge`, `TypeBadge`, `TypeChip` — approval view rendering
- `BrandMark` — logo rendering
- `routes/+error.svelte`, `routes/+layout.svelte`, `routes/approve/[id]/+page.svelte` — page routing and layouts
- `lib/auth.ts`, `lib/branding.svelte.ts`, `lib/session.svelte.ts` — client-side state
- Error states (401/403/404/5xx), empty lists, loading states
- Keyboard interaction (modals, menu focus)
- Theme toggle persistence

## E2E Testing Gap (Phase 1)

### Existing E2E Tests (11 tests total)

**Tier 1 (wire, 6 tests)**:
1. `TestLogin_PrintsApprovalURLBeforeCompletion` — approval URL printed while process waits
2. `TestLogin_ApprovingDeliversCertificateOverSSE` — approval delivers certificate
3. `TestLogin_CertificateCarriesOnlyPermittedExtensionsAndNoCriticalOptions` — server narrows extensions
4. `TestLogin_DenyingResolvesWithNoCertificate` — denial returns clean error
5. `TestLogin_SecondLoginReusesValidCertificateWithoutNewApproval` — certificate reuse
6. `TestLogout_RemovesOnlySsoosshCertificateLeavingUnrelatedKeyUntouched` — logout only removes our cert

**Tier 2 (browser, 3 tests)**:
1. `TestApproval_UnauthenticatedVisitorSeesSignInNotApprove` — unauthenticated sees login button ✓ (exists)
2. `TestApproval_TrimmedOptionsShownStruckThroughBeforeApproval` — narrowed extensions shown struck-through ✓ (exists)
3. `TestApproval_SecondIdentityOpeningSameLinkIsRefused` — second user on same link denied ✓ (exists)

**Tier 3 (sshd, 2 tests)**:
1. `TestSSH_SshdAcceptsTheIssuedCertificate` — certificate authenticates session
2. `TestSSH_AfterLogoutTheSameSSHIsRefused` — post-logout ssh is rejected

### Critical Gaps (Not Yet Covered)

These are the minimum scenarios from the mission spec that must be added:

#### Auth/Identity Errors (Tier 1)
- Unauthenticated approve attempt → 401/403
- Expired/invalid session cookie → redirect to login
- OIDC state mismatch → provider error
- OIDC nonce mismatch → provider error
- IdP returning error response → error display
- IdP token with bad signature → auth failure
- IdP token with wrong audience → auth failure
- Token missing required claims (e.g., `sub`, `groups`) → auth failure
- Group claim not authorizing (empty group never authorizes) → approval denied
- Admin accessing auditor-scoped routes (commit 0fa6f87 added this) → verify end-to-end

#### Authorization (Tier 2)
- Non-admin hitting admin routes → 403
- Admin accessing auditor-scoped routes → verify the new feature works
- Empty group never authorizes (already in code) → verify in approval flow

#### Certificate Request Lifecycle (Tier 1)
- Request expiry while client waits → timeout, client sees error
- Client disconnect/reconnect during wait → reconnect picks up same request
- Duplicate/concurrent requests from one identity → deduplication works
- Certificate reuse when still valid → already covered, needs explicit duration test
- Forced re-approval when expired → new approval needed
- Short-lifetime cert to cover expiry edge case → cert expires, next login needs new cert
- Options/extensions trimmed before approval → already covered (tier 2)
- Request for forbidden extension → server rejects, shows narrowed options

#### SSE / Streaming (Tier 1)
- Malformed/interrupted SSE stream → client recovers or fails gracefully
- Server restart mid-stream → client reconnect or error
- Client timeout during long wait → timeout, error message

#### Security Middleware (Tier 2)
- Cross-origin POST → CSRF rejection (Sec-Fetch-Site header)
- CSP violations → confirm page loads without console errors
- HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy → verify headers present
- SPA's own JS loads without CSP violation → real browser test

#### Rate Limiting (Tier 1)
- Global rate limiter trips → 429 status
- Endpoint-specific limiter trips → 429 status
- Limiter recovers after window expires → request succeeds
- Verify both limiters are wired and have effect

#### Host Certificates (Tier 1)
- `host sign` happy path and rejection (bad key, wrong format)
- `host renew` success and error paths
- `host sync` synchronizes multiple hosts
- `host principals` lists principals

#### Service Enrollment (Tier 1)
- `service enroll` creates enrollment token
- `service retrieve` uses token to get cert
- Bad enrollment token → error
- Wrong host key → error
- Expired enrollment → error

#### Client CLI (Tier 1)
- `ssh login` happy path (covered as part of e2e)
- `ssh logout` removes ssoossh cert only (covered)
- `ssh config` displays configuration
- `ssh inspect` displays certificate
- `ssh proxycommand` works in SSH chain
- `version` command works
- `principals` command lists principals
- **Offline commands** (commit 0fa6f87 added `Offline` flag):
  - `ssh login --offline` → must not contact server
  - `version --offline` → must not contact server
  - `principals --offline` → must not contact server
- Error paths: no server reachable, TLS failure, malformed config, missing ssh-agent, agent refuses key

#### SSH Server (Tier 3)
- Certificate accepted and session opens (covered)
- Post-logout ssh rejected (covered)
- Certificate with wrong principal rejected
- Expired certificate rejected
- Certificate from untrusted CA rejected

#### Server Lifecycle (Tier 1)
- Graceful shutdown mid-request → requests in-flight handled
- Failed migration at startup → clear error, not panic
- Bad config at startup → clear error, not panic

## Priorities

### P0 (E2E: Critical Path)
- Missing e2e tests that prove the core flow works end-to-end under error conditions (auth failures, timeouts, denials)
- Server model types (database-backed data structures)
- Cert message channel (`server/certmsg`) — drives approval delivery

### P1 (Unit: High Value)
- Admin routes (all 0% — new feature from commit 0fa6f87)
- Config type methods (FIPSEnabled, IsAdminEnabled, etc. — all 0%)
- Client command execution paths (Run, Execute, newAPIClientFromConfig — all 0%)
- Service and certification request error paths

### P2 (Unit: Medium Value)
- Bootstrap edge cases (pool config, migration errors)
- API error response handling
- Plist parsing edge cases (macOS policy loading)
- CLI output formatting

### P3 (Frontend)
- Component rendering
- Error states and edge cases
- User interactions (keyboard, focus)
- Theme persistence

## Notes for Implementation

1. **E2E tier discipline**: Tier 1 tests should drive most error paths via HTTP; tier 2 adds browser-specific scenarios; tier 3 only adds sshd-specific assertions.
2. **`data-testid` attributes**: Frontend needs stable selectors for tier 2 tests. Add to all elements currently matched by prose.
3. **Offline flag**: Recent commit 0fa6f87 added an `Offline()` method to commands — verify this flag truly skips all network calls.
4. **Admin/auditor authorization**: Commit 0fa6f87 added admin as parent of auditor — cover the full hierarchy end-to-end.
5. **Coverage exclusions**: All entries in `exclude-from-coverage.txt` are documented with specific line ranges — no additional exclusions should be added without justification.

