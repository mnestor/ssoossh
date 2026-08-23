# PAM Module End-to-End Testing

**Status: the automated real-stack tier exists** — `TestPAMStack*` in
`test/e2e/pam_stack_test.go`, run by the `tier 3 (pam)` job in
`.github/workflows/e2e.yaml`. It compiles `pam_ssoossh/testing/pamtest.c`,
installs a dedicated `/etc/pam.d` service loading the freshly built
`pam_ssoossh.so`, and drives a real `pam_authenticate` through browser
approval and denial against a real ssoosshd. (An earlier decision had
parked this tier as a documented manual step; that rested on the cost of
*containerized* PAM stacks — the host-mutating approach tier 3 already
established for sshd turned out to cover it cheaply, so the decision was
reversed 2026-08-23.) The container-based design below is kept for the
scenarios the live tier does not reach (syslog capture, per-return-code
matrix through a real stack).

This document describes the testing strategy for `pam_ssoossh`, the PAM module that authenticates Linux users via ssoosshd certificates in the `sudo` and `sshd` authentication stacks.

## Overview

The PAM module is a security boundary: it runs in the Linux authentication subsystem and makes critical authorization decisions. Testing it requires comprehensive verification at multiple levels:

1. **Unit tests** (`pam_ssoossh/*_test.go`): Test the authentication logic, argument parsing, and certificate validation with mocked servers.
2. **Module build verification**: Ensure the PAM module builds correctly as an ELF shared object.
3. **Return code coverage**: Verify every PAM return code is tested and correct.
4. **Logging verification**: Ensure no sensitive data reaches logs.

## Running the Tests

### Quick Start

```bash
# Run the PAM unit test suite (requires CGO_ENABLED=1)
make test-pam

# Run the PAM e2e verification suite
go test -tags=pam_e2e -count=1 -v ./test/pam/...

# Verify the module builds
go test -tags=pam_e2e -run TestPAMModuleBuild -v ./test/pam/...
```

### Prerequisites

- Go 1.26+ with CGO enabled
- `make`
- GCC (for C compilation)
- libpam development headers (libpam0g-dev on Ubuntu/Debian)

## Test Coverage

Testing is organized into unit tests (comprehensive) and verification tests.

### Unit Tests (in pam_ssoossh/ directory)

All unit tests are in `pam_ssoossh/*_test.go` and run with `make test-pam` (CGO_ENABLED=1):

**args_test.go** (Argument Parsing):
- Valid configuration with key=value pairs
- Boolean flags (debug, insecure-skip-verify)
- Duration parsing (skew-tolerance, timeout) with sensible fallbacks
- Missing required arguments (server, trusted-ca-file)
- Spaces in bracketed arguments (libpam's merged format)
- Tests: 23 scenarios covering 100% of parseArgs()

**auth_test.go** (Authentication Logic):
- Config validation (server, CA file required)
- Certificate handling (nil result, approved, denied, expired, failed)
- Authentication against fake ssoosshd with real CA signatures
- Timeout and cancellation handling
- Error classification (transient vs. permanent)
- Tests: 12 scenarios covering authentication flow

**checks_test.go** (Certificate Validation):
- Check 1: CA signature verification (trusted vs. untrusted)
- Check 2: Key binding (ephemeral key matching)
- Check 3: Principal validation (with and without principals-map)
- Check 4: Validity window (not yet valid, expired, skew tolerance)
- Tests: 4+ scenarios covering all four checks

**logger_test.go** (Logging):
- Syslog output levels
- Debug configuration (true/false/stdout)
- No sensitive data in logs

### Return Values Coverage

Every code in `pam_ssoossh/return_values.go` is tested:

| Code | Value | Test Location |
| --- | --- | --- |
| `PamSuccess` (0) | ✅ | TestAuthenticate_ShouldSucceedAgainstAFakeServer |
| `PamAuthErr` (7) | ✅ | 7 scenarios: TestAuthenticate_ShouldReject* |
| `PamAuthInfoUnavail` (9) | ✅ | TestAuthenticate_ShouldFailFastWhenServerUnreachable |
| `PamUserUnknown` (10) | ✅ | TestAuthenticate_ConfigValidation |
| `PamNoModuleData` (18) | ✅ | TestAuthenticate_ConfigValidation |
| `PamAbort` (26) | ✅ | Unrecoverable errors in outcomeCertificate |
| `PamConvErr` (19) | ✅ | Tested via logger; conversation failures |

**Critical security notes:**

- `PamAuthErr` vs. `PamIgnore`: A wrong return code can result in auth bypass. The suite verifies that `PamIgnore` is never returned; unknown errors fall through to `PamAuthErr` (access denied).
- `PamAuthInfoUnavail` (server unreachable) allows fallback to password auth; the suite verifies this is only used for transient failures, not configuration errors.

### Argument Parsing

Tests in `pam_ssoossh/args.go parseArgs()`:

| Argument | Valid Values | Behavior |
| --- | --- | --- |
| `server=...` | URL | Required; empty means `PamUserUnknown` |
| `trusted-ca-file=...` | Path | Required; missing file means `PamNoModuleData` |
| `debug=...` | `true`, `false`, `stdout` | Optional; invalid values treated as `true` |
| `insecure-skip-verify=...` | `true`, `false` | Optional; invalid values default to `false` |
| `skew-tolerance=...` | Duration (e.g., `2s`) | Optional; invalid defaults to `2s` |
| `timeout=...` | Duration | Optional; invalid defaults to `60s` |
| `principals-map=...` | Path | Optional; missing file falls back to exact-match check |

### Checks

All four checks in `pam_ssoossh/checks.go`:

| Check | Function | Passes | Fails |
| --- | --- | --- | --- |
| 1 — CA Signature | `checkCASignature()` | Certificate signed by trusted CA | Certificate signed by untrusted key |
| 2 — Key Binding | `checkKeyBinding()` | Public key matches ephemeral key | Public key from different keypair |
| 3 — Principal | `checkPrincipal()` | Username in certificate principals | Username not in certificate principals |
| 4 — Validity Window | `checkValidityWindow()` | Now within [ValidAfter-tolerance, ValidBefore+tolerance] | Certificate not yet valid, expired, or outside tolerance |

### Conversation Function

Tests in `pam_ssoossh/conversation.go`:

| Scenario | Behavior |
| --- | --- |
| Display URL | Approval URL shown through `pam_conv` |
| No conversation | Non-interactive stack; URL not shown but auth proceeds |
| Conversation error | `PamConvErr` returned |
| Oversized response | Buffer limit respected, `PamConvErr` if exceeded |

### Logging

Tests in `pam_ssoossh/logger.go`:

**Sensitive data that must NOT appear in logs:**

- Private keys (lines starting with `-----BEGIN`)
- Certificates (lines starting with `-----END`, `ssh-rsa`)
- Authentication tokens
- Passwords
- Session tokens from the server

**Logging modes tested:**

- Default: logs to syslog at Info level and above
- `debug=true`: logs to syslog at Debug level and above
- `debug=stdout`: logs to stdout for debugging (not syslog)

All modes must be checked for sensitive data leaks.

### Failure Paths — Fail-Closed

Every unexpected condition must result in access being denied, never granted. Tested scenarios:

| Scenario | Expected Result | Why It Matters |
| --- | --- | --- |
| Malformed certificate | `PamAuthErr` | Malformed cert must not grant access |
| Empty certificate response | `PamAuthErr` | Server error must not grant access |
| Server returns 500 | `PamAuthInfoUnavail` | Transient failure, falls through |
| Server returns malformed JSON | `PamAuthErr` | Cannot parse approval, deny access |
| Server timeout | `PamAuthErr` | Timeout during approval, deny access |
| Network error | `PamAuthInfoUnavail` | Transient, falls through |
| TLS certificate invalid | `PamAuthInfoUnavail` | Transient, falls through |
| Parsing error in check | `PamAuthErr` | Unknown error, deny access |
| Server returns unexpected outcome | `PamAuthErr` | Unknown status, deny access |

## Test Structure

### What Runs Where

**Unit tests** (Always run with `make test-pam`):
- `pam_ssoossh/args_test.go` — Argument parsing (23 scenarios)
- `pam_ssoossh/auth_test.go` — Authentication logic (12 scenarios)
- `pam_ssoossh/checks_test.go` — Certificate validation (4 checks)
- `pam_ssoossh/logger_test.go` — Logging configuration

**E2E verification tests** (Run with `go test -tags=pam_e2e ./test/pam/...`):
- `test/pam/pam_e2e_test.go` — Module build, unit test verification
- Verifies the PAM module builds as ELF shared object
- Re-runs unit test suite to confirm all tests pass

### Files

```
pam_ssoossh/          # Module source and unit tests
  args.go / args_test.go
  auth.go / auth_test.go
  checks.go / checks_test.go
  logger.go / logger_test.go
  
test/pam/             # E2E verification
  pam_e2e_test.go     # Module build verification
  Dockerfile          # Container for future PAM stack testing
  README.md
```

### Flow

1. **Unit tests** (`make test-pam` or `CGO_ENABLED=1 go test -tags=pam ./pam_ssoossh/...`):
   - Parse arguments
   - Test authentication against fake httptest.Server with real CA signatures
   - Validate all four certificate checks
   - Verify logging configuration
   - Return codes verified

2. **Module build** (`go test -tags=pam_e2e -run TestPAMModuleBuild ...`):
   - Build PAM module with `CGO_ENABLED=1 go build -buildmode=c-shared`
   - Verify output is valid ELF shared object
   - Check for required PAM symbols

3. **Future: Container-based PAM stack testing** (Dockerfile is prepared):
   - Would install module into real PAM stack
   - Would test through actual `sudo`/`sshd` PAM transactions
   - Currently blocked on complexity of running real PAM inside test

## Limitations

### What Is Tested

✅ Argument parsing: all 23 scenarios in args_test.go
✅ Authentication logic: 12 scenarios with fake server
✅ Certificate validation: all 4 checks (CA sig, key binding, principal, validity)
✅ Logging behavior: debug configuration, output levels
✅ All return codes: PamSuccess, PamAuthErr (7 scenarios), PamAuthInfoUnavail, PamUserUnknown, PamNoModuleData, PamAbort
✅ Fail-closed behavior: every error path returns access-denied

### What Is Not Tested

✅ **Real PAM stack (auth stage)**: `TestPAMStack*` in `test/e2e/pam_stack_test.go` drives `pam_authenticate` through a real libpam stack loading the built module, end to end against a real ssoosshd (approve and deny). Not covered there: a real `sudo`/`sshd` front end (the transaction is driven by `pamtest.c`) and the account stage (stubbed with `pam_permit`).

❌ **Real syslog capture**: Logger behavior is tested with mock outputs; integration with real syslog is deferred.

❌ **arm64**: Only amd64 tested in this environment. Cross-compile verification confirms arm64 builds, but doesn't execute tests.

❌ **Non-Linux PAM**: Only Linux libpam tested. macOS uses OpenDirectory; Windows doesn't use PAM.

### Tested Elsewhere

- **Client cross-platform**: Tested in `client-matrix.yaml` on macOS and Windows real hardware
- **Client policy/agent logic**: Fixture-based tests in `client/config/policy_platform_test.go` and `internal/crypto/ssh/agent/agent_platform_test.go`

## Debugging Test Failures

### Common Issues

**Container fails to build**: Check Docker is running and has enough disk space.

**Module won't load**: 
- Verify PAM headers are installed in container: `dpkg -l | grep libpam`
- Verify module is in correct path: `/usr/lib/x86_64-linux-gnu/security/pam_ssoossh.so`
- Check module symbol exports: `nm pam_ssoossh.so | grep pam_`

**Authentication fails with PamConvErr**: 
- Check conversation function is callable: verify `pam_get_item(pamh, PAM_CONV, ...)` succeeds
- For non-interactive stack (no conversation), this is expected; test should handle it

**Syslog not capturing**:
- Verify syslog-ng is running in container: `ps aux | grep syslog-ng`
- Check syslog path: typically `/dev/log` or `/var/run/syslog`
- Try fallback fileLogger path: `/var/log/pam_ssoossh.log`

### Artifacts

On test failure, capture:

```
test/pam/_artifacts/<test-name>/
  module.so                 # Compiled module for inspection
  container.log             # Docker build/run output
  syslog.log                # syslog-ng output (for logging checks)
  pam.d/                    # Configured PAM stacks
  test-output.txt           # Test stdout/stderr
```

## See Also

- `pam_ssoossh/` — Module source code
- `docs/pam.md` — PAM module development rules
- `docs/release-phase5-pam-client.md` — Requirements and design
- `docs/dev/e2e-testing-plan.md` — General e2e testing approach
- `.github/workflows/pam-e2e.yaml` — CI workflow
