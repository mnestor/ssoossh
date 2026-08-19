---
paths:
  - "**/*.go"
---

## ssoossh-Specific Facts

Project-specific detail split out of `go.md`/`test-go.md` so those stay
generic and portable to other repos. See also `pam.md`, `client-cmd.md`,
`database.md`, `server-api.md`, `server-config.md`, `server-db.md` for
more narrowly-scoped specifics.

- Module path is `github.com/mnestor/ssoossh`. `golangci-lint`'s
  `goimports` formatter is configured with `local-prefixes:
  github.com/mnestor/ssoossh` — that's what enforces the 3-way import
  grouping described in `go.md`, but only via `golangci-lint fmt`/`run
  --fix`, not a bare editor `goimports`.
- The top-level product/service packages `go.md`'s cross-package rule
  refers to are `server/`, `client/`, and `pam_ssoossh/` — they must not
  import each other directly, only through `internal/`.
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
  `pam_ssoossh/pam.go`, `pam_ssoossh.go`, and `conversation.go` document
  why their cgo entry points have no Go test and point at
  `pam_ssoossh/testing/pamtest.c` as the manual harness. See `pam.md`.
