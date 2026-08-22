# PAM Module End-to-End Testing

This document describes the containerized e2e test suite for `pam_ssoossh`, the PAM module that authenticates Linux users via ssoosshd certificates in the `sudo` and `sshd` authentication stacks.

## Overview

The PAM module is a security boundary: it runs in the Linux authentication subsystem and makes critical authorization decisions. Testing it requires:

1. **Real PAM stack**: The module cannot be tested by mocking libpam; it must run inside an actual PAM transaction against real PAM stacks (`sudo`, `sshd`).
2. **Real syslog**: Logging behavior must be validated to ensure no sensitive data (tokens, certificates, private keys) reaches system logs.
3. **Real OpenSSH**: The `sshd` stack validation requires a real sshd configured to trust the test CA.
4. **Containerization**: All of the above run in a Docker container (`test/pam/Dockerfile`) to avoid host pollution and ensure hermetic, reproducible runs.

## Running the Tests

### Quick Start

```bash
# Run the full PAM e2e suite
make test-pam-e2e

# Run just the happy path
make test-pam-e2e ARGS="-run TestPAMHappyPath"

# Run with verbose output
make test-pam-e2e ARGS="-v"
```

### Prerequisites

- Docker (verified working during setup)
- Go 1.26+
- `make`

## Test Coverage

The suite covers the following scenarios, organized by category:

### Happy Path

| Scenario | Stack | Expected Outcome |
| --- | --- | --- |
| Valid certificate, matching principal | sudo | Access granted, `PamSuccess` |
| Valid certificate, matching principal | sshd | Access granted, `PamSuccess` |

### Return Values

Every code in `pam_ssoossh/return_values.go` has a test scenario:

| Code | Value | Scenario | Security Implication |
| --- | --- | --- | --- |
| `PamSuccess` | 0 | Valid certificate | Access granted |
| `PamAuthErr` | 7 | No certificate, expired, wrong principal, denied, timeout | Access denied |
| `PamAuthInfoUnavail` | 9 | Server unreachable, TLS failure | Fall through to next auth method |
| `PamUserUnknown` | 10 | Server not configured | Authentication not applicable |
| `PamNoModuleData` | 18 | CA file not configured or missing | Authentication not applicable |
| `PamAbort` | 26 | Unrecoverable error (e.g., API client setup failure) | Abort entire stack |
| `PamConvErr` | 19 | Conversation function error, response oversized | Cannot display approval URL |

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

## Architecture

### Files

```
test/pam/
  Dockerfile         # Test environment container
  README.md          # This directory's purpose
  pam_e2e_test.go    # Main test suite
```

### Test Environment (Dockerfile)

The container includes:

- Ubuntu 22.04 base
- PAM libraries (`libpam0g`, `libpam0g-dev`)
- OpenSSH server and client
- syslog-ng for capturing logs
- sudo
- Build tools for compiling the module
- Go toolchain (injected at runtime)

### Flow

1. **Build module**: `pam_ssoossh.so` built with `CGO_ENABLED=1 go build -buildmode=c-shared`
2. **Build container**: Docker image built from `test/pam/Dockerfile`
3. **Run tests**:
   - Start container
   - Copy module into container at `/usr/lib/x86_64-linux-gnu/security/pam_ssoossh.so`
   - Configure PAM stacks:
     - `/etc/pam.d/sudo`: add ssoossh module
     - `/etc/pam.d/sshd`: add ssoossh module
   - Start syslog-ng inside container
   - For each test scenario:
     - Set up test conditions (expired cert, wrong principal, etc.)
     - Call `pam_sm_authenticate` (or invoke `sudo`/`ssh` to trigger it)
     - Verify return code and behavior
     - Capture syslog output, verify no sensitive data
   - Teardown: stop container

## Limitations and Future Work

### Not Covered Here

1. **arm64**: Only amd64 is tested in this suite. arm64 would require a separate container or cross-compilation verification.
2. **Other distributions**: Only Ubuntu 22.04. Debian, Fedora, Alpine would need separate containers.
3. **Other PAM implementations**: Only Linux libpam. macOS uses a different PAM version; Windows does not use PAM.
4. **Pageant (Windows) and macOS keychain integration**: Platform-specific agent handling is tested separately via the client-matrix CI workflow on real hardware.

### What Is Validated Elsewhere

- **Cross-platform client compilation**: Verified in `client-matrix.yaml` CI workflow across amd64, arm64 on linux/darwin/windows.
- **Cross-platform agent integration**: Tested natively on macOS and Windows in `client-matrix.yaml`.
- **Policy path logic**: Testable on Linux with fixture data in `client/config/policy_platform_test.go`.

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
- `docs/e2e-testing-plan.md` — General e2e testing approach
- `.github/workflows/pam-e2e.yaml` — CI workflow
