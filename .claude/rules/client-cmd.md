---
paths:
  - client/cmd/**/*.go
---

## Client CLI Rules

- Command tree is `bep/simplecobra`, not raw `spf13/cobra` — every command
  is a `simplecobra.Commander` implementation (`Name`, `Commands`, `Init`,
  `PreRun`, `Run`), assembled as a tree, not registered onto a shared
  `*cobra.Command` imperatively.
- `RootCommand` (`client/cmd/cmd.go`) holds the shared state — config, API
  client, SSH agent — behind `Config()`/`API()`/`Agent()`. Leaf and group
  commands reach it at runtime via `cd.Root.Command.(*RootCommand)`, not
  through an import-time dependency or a constructor argument.
- New leaf commands follow the `newXCommand() simplecobra.Commander`
  factory pattern (see `ca.go`, `host_sign.go`, `host_principals.go`);
  command groups nest child commands the same way (`service.go`).
- Simple leaf commands that don't need custom `Commander` boilerplate can
  use `simpleCommand` (`simplecommand.go`) instead of writing a new type.
