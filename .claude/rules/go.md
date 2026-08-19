---
paths: 
  - **/*.go
---

Always run `go` commands from the root project 

## Cross-Package Rules
- Breaking changes to shared types require updating all go consumers
- Don't import directly between top-level product/service packages (e.g.
  separate binaries or deployables in the same module) — go through a
  shared `internal/` package

## Key Conventions
- **Interface-based design**: prefer interfaces for testability
- **Table-driven tests** with `bytes.Buffer` for capturing stdout/stderr; test files are colocated with source
- Coverage exclusions are listed in `exclude-from-coverage.txt`
- Follow standard Go project layout — `cmd/` for entrypoints, `internal/` for
  private packages shared in this repo
- Use `context.Context` as the first parameter in all functions that do I/O
- Return errors, do not panic — `return fmt.Errorf("send email: %w", err)`
- Wrap errors with context using `%w` for unwrapping
- Logging: slog (structured, JSON in prod)
- No global state — pass dependencies through structs, preferably interfaces. slog is the only exception
- Use interfaces for external dependencies (SMTP client, database) to enable testing
- Interfaces defined in the package that implements them, not the package that uses them
- Imports go in 3 blank-line-separated groups: standard lib, external, local
  (your module's own path). `.golangci.yml` sets `goimports`' `local-prefixes`
  to `github.com/mnestor/ssoossh`, so `golangci-lint run --fix` enforces this.
- **Struct-based DI**: hold shared dependencies (config, clients, loggers,
  DB handles) on a struct built once at startup, and construct
  subcommands/handlers/steps as methods on that struct so they get direct,
  typed access to shared state. Don't pass dependencies as loose function
  arguments and don't stash them in `context.Context` values. Reserve
  `context.Context` for genuinely request-scoped data: cancellation,
  deadlines, trace IDs — not for DI.

## Before Pushing
- Run `golangci-lint run` and `go test ./...`

## Do NOT

- Do not use `init()` functions
- Do not use package-level variables for state
- Do not use `interface{}` — use `any` or define proper types