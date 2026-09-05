---
paths:
  - internal/**/*.go
---

## Keeping the Shared Packages PAM-Safe

The `pam_ssoossh` PAM module lives in its own project,
[github.com/mnestor/ssoossh-pam](https://github.com/mnestor/ssoossh-pam). It
is not built here, but it consumes the same wire contract and mirrors the
same shared logic, so two constraints still bind code in `internal/`.

- **Never write to stdout or stderr.** The module is loaded into `sudo` and
  `sshd`; writing to either stream corrupts the host process's own output and
  can leak into the PAM conversation. It logs through its own syslog logger
  and never calls `slog.SetDefault`, so inside PAM `slog.Default()` is Go's
  built-in handler, which writes to **stderr**. That makes `slog.Default()`
  unusable in any `internal/` package the module mirrors: `internal/api`,
  `internal/crypto/ssh/keypair`, `internal/fipsmode`,
  `internal/principalsmap`, and `internal/version`. Such a package must take
  an explicit `*slog.Logger` and do nothing when it is nil — see
  `api.Config.Logger` and `installRequestTracing`. Relying on a level being
  below the default handler's threshold is not sufficient; it breaks silently
  the moment someone adds a higher-level call. Client-only packages
  (`client/...`, `internal/crypto/ssh/agent`) may use `slog.Default()`.
- **Watch the dependency weight.** The module is a c-shared object mapped
  into every `sudo` invocation, so the packages above are deliberately built
  on the standard library. `internal/principalsmap` hand-parses a YAML subset
  rather than linking `gopkg.in/yaml.v3` (549 KB), and `internal/api` is built
  on `net/http` rather than a convenience library (726 KB). Adding a
  dependency to one of these packages is a decision, not a detail.
- **Wire changes are cross-repository.** `internal/apitypes` and the
  `internal/principalsmap` file format are contracts the module also speaks.
  A breaking change here needs a matching change in `ssoossh-pam`; see
  `docs/internals/wire-types.md`.
