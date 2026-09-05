---
paths:
  - "**/*.go"
---

## ssoossh-Specific Facts

Project-specific detail split out of `go.md`/`test-go.md` so those stay
generic and portable to other repos. See also `wire-contract.md`,
`client-cmd.md`, `database.md`, `server-api.md`, `server-config.md`,
`server-db.md` for more narrowly-scoped specifics.

- Module path is `github.com/mnestor/ssoossh`. `golangci-lint`'s
  `goimports` formatter is configured with `local-prefixes:
  github.com/mnestor/ssoossh` — that's what enforces the 3-way import
  grouping described in `go.md`, but only via `golangci-lint fmt`/`run
  --fix`, not a bare editor `goimports`.
- The top-level product/service packages `go.md`'s cross-package rule
  refers to are `server/` and `client/` — they must not import each other
  directly, only through `internal/`. The `pam_ssoossh` PAM module is a
  separate project (github.com/mnestor/ssoossh-pam), written in C, and
  shares no code with this repository at all; see `wire-contract.md` for the
  two contracts that still cross that boundary.
- Struct-based DI has two different concrete shapes here:
  - Server: `server/bootstrap` wires config, db, router, and scheduler
    onto an unexported `app` struct (`server/bootstrap/bootstrap.go`);
    startup steps are methods on `*app` (e.g. `a.initLogging()`).
  - Client: `client/cmd` uses `bep/simplecobra`, not a hand-rolled `app`
    struct. A `RootCommand` implements `simplecobra.Commander` and holds
    config/API client/SSH agent behind `Config()`/`API()`/`Agent()`.
    Commands reach it at runtime via `cd.Root.Command.(*RootCommand)`.
    See `client-cmd.md`.
- Concrete example of the untestable-code policy from `test-go.md`:
  `server/signer`'s PKCS#11 paths carry `not covered:` comments where a
  block needs a live HSM token, and `make test-hsm` (softhsm2, behind the
  `softhsm` tag) is the suite that does reach them.
