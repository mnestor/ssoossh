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
  Document why in a `not covered:` comment on the function, the way
  `pam.go`'s `GetUser` and `pam_ssoossh.go`'s `authenticate` already do.
  Pull the actual logic (arg parsing, auth decision)
  out into a plain Go function the cgo wrapper calls, and unit test that
  function directly (see `args_test.go`, `auth_test.go`).
- `pam_ssoossh/testing/pamtest.c` is the manual harness for exercising cgo
  paths against a real PAM stack — update it when the cgo surface changes,
  it won't be caught by `go test`.
- Log the module version at Info on every invocation, unconditionally (not
  gated behind debug) — a module that can only report its version with
  debug already enabled is a worse support problem than one extra log line
  per auth attempt.
- **Never write to stdout or stderr from anything pam_ssoossh can reach.**
  The module is loaded into `sudo` and `sshd`; writing to either stream
  corrupts the host process's own output and can leak into the PAM
  conversation. pam_ssoossh logs through its own `Logger` (`logger.go`) to
  syslog, and never calls `slog.SetDefault` — so inside PAM, `slog.Default()`
  is Go's built-in handler, which writes to **stderr**.
  That makes `slog.Default()` unusable in any `internal/` package pam
  imports: today `internal/api`, `internal/crypto/ssh/keypair`,
  `internal/fipsmode`, `internal/principalsmap`, and `internal/version`.
  Such a package must take an explicit `*slog.Logger` and do nothing when it
  is nil — see `api.Config.Logger` and `installRequestTracing`. Relying on a
  level being below the default handler's threshold is not sufficient; it
  breaks silently the moment someone adds a higher-level call.
  Client-only packages (`client/...`, `internal/crypto/ssh/agent`, which pam
  does not import) may use `slog.Default()`. If pam ever needs one of those,
  it needs the explicit-logger treatment first.
