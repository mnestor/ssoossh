# PAM End-to-End Testing

This directory contains the containerized e2e test suite for `pam_ssoossh`, the PAM module that authenticates users via ssoosshd certificates.

## Files

- `Dockerfile` — testing environment with PAM, OpenSSH, and syslog
- `pam_test.go` — e2e test suite (generated, runs inside a container)

## Running Tests

See `docs/pam-e2e-testing.md` for full documentation.

Quick start:

```bash
make test-pam-e2e          # Build module, create container, run suite
```

## Architecture

The test harness:
1. Builds the `pam_ssoossh.so` module
2. Creates a Docker container with a real PAM stack
3. Installs the module into the PAM configuration
4. Tests authentication scenarios: valid certificate, expired cert, wrong principal, etc.
5. Captures syslog output to verify no sensitive data is logged
6. Verifies each PAM return code

## What Is Tested

- Happy path: valid certificate authentication through sudo and sshd
- Every return value in `pam_ssoossh/return_values.go`
- Argument parsing: valid, unknown, malformed, missing required, conflicting
- All four checks in `pam_ssoossh/checks.go`
- Conversation function behavior
- Logging: no tokens, no certificates, no private material
- Failure paths: no certificate, expired, wrong principal, revoked session, server unreachable, TLS failure

## Limitations

- Runs on amd64 only (arm64 not validated here)
- Tests the Linux PAM stack (other platforms would need different containers)
