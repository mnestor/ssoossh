---
paths:
  - pam_ssoossh/**/*.go
  - pam_ssoossh/**/*.c
---

## PAM Module Rules

- Every Go file in this package is `//go:build pam` and cgo (`import "C"`);
  build/test/lint all need the tag and `CGO_ENABLED=1`: `make pam`,
  `make test-pam`, `make lint-pam`. Plain `go build ./...` skips this
  package entirely.
- cgo entry points (`pam.go`, `pam_ssoossh.go`) are thin wrappers around a
  live `pam_handle_t` and have no Go unit test — that's expected, not a gap.
  Document why in a comment and list the exact lines in
  `exclude-from-coverage.txt`, same as the existing entries for `pam.go`
  and `pam_ssoossh.go`. Pull the actual logic (arg parsing, auth decision)
  out into a plain Go function the cgo wrapper calls, and unit test that
  function directly (see `args_test.go`, `auth_test.go`).
- `pam_ssoossh/testing/pamtest.c` is the manual harness for exercising cgo
  paths against a real PAM stack — update it when the cgo surface changes,
  it won't be caught by `go test`.
- Log the module version at Info on every invocation, unconditionally (not
  gated behind debug) — a module that can only report its version with
  debug already enabled is a worse support problem than one extra log line
  per auth attempt.
