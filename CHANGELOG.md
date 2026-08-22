# Changelog

All notable changes to ssoossh are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

This changelog is automatically generated from [Conventional Commits](https://www.conventionalcommits.org/)
using [git-cliff](https://github.com/orhun/git-cliff).

To regenerate this file after adding commits, run:

```bash
make changelog
```

---

## [Unreleased]

### Added

- Shell completion subcommands for bash, zsh, fish, powershell (both client and server)
- Flag completion for `--config` paths in client and server
- Man page generation tool (`internal/tools/gendocs`)
- Automatic man page regeneration with `make gendocs`
- Git-cliff configuration (`cliff.toml`) for changelog automation
- Dependency scanning formalization with `.semgrep.yaml`
- CONTRIBUTING.md and AGENTS.md for human contributors
- Documentation for dependency scanning (`docs/DEPENDENCY-SCANNING.md`)

### Changed

- Server command migrated from spf13/cobra to bep/simplecobra (matching client architecture)

### Security

- Formalized security scanning configuration with explicit severity gates
- Added `.semgrep.yaml` for SAST scanning

## Detailed Commit History

For a complete list of all commits and their details, see the [git log](https://github.com/mnestor/ssoossh/commits/main).

To see changes between versions:

```bash
git log v1.0.0..v2.0.0 --oneline
```

---

**Note:** This CHANGELOG is generated from git commit messages. Ensure your commits follow the
[Conventional Commits](https://www.conventionalcommits.org/) format for proper categorization:

- `feat:` — new features (appears under "Added")
- `fix:` — bug fixes (appears under "Fixed")
- `docs:` — documentation (appears under "Changed" or separate section)
- `perf:` — performance improvements (appears under "Performance")
- `refactor:` — code refactoring (appears under "Changed")
- `test:` — test additions/updates (appears under "Testing")
- `chore:` — build, CI, etc. (typically excluded from changelog)

See `cliff.toml` for the full configuration.
