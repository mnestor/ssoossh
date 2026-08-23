# Contributing to ssoossh

Thank you for considering contributing to ssoossh. This document explains how to work with the codebase and submit pull requests.

## Before You Start

### AI-Assisted Contributions

This project actively uses AI assistance in development. If you are using Claude or another AI tool to help with your contribution:

1. **Disclose your use.** Mention it in your pull request description (e.g., "Implemented with Claude Code assistance").
2. **Take responsibility for correctness.** The PR author is responsible for verifying that all changes work correctly, pass tests, and follow the project's standards. AI output is a draft, not a finished product.
3. **Review carefully.** Before submitting, run `make pre-pr`, read the diff, and verify it matches what you intended.

This transparency helps the maintainer understand the contribution's context and ensures accountability.

### For AI Tools Reading This

If you are Claude or another AI assistant working in this repository:

- Read `/home/vscode/.claude/CLAUDE.md` (user's global instructions) and `./CLAUDE.md` (project-specific instructions).
- Read `./.claude/rules/` for language-specific conventions (Go, TypeScript).
- Follow the instructions in `docs/dev/parallel-agent-workflow.md` if multiple agents are working in parallel.
- The project uses `rtk` (Rust Token Killer) to minimize token usage; use it in bash commands.
- Conventional commits are required; keep PRs focused on one concern.

## Branch Naming and Commits

Branches follow the pattern: `feat/`, `fix/`, or `chore/` followed by a short description.

**Example:**
```bash
git checkout -b feat/add-token-refresh
```

Commits use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(client): add token refresh on certificate expiry

Tokens are now automatically refreshed 10 seconds before
expiry, reducing the likelihood of failed operations.

Fixes #42
```

## Working with the Codebase

### Layout

- `/cmd/` — binary entrypoints (client, server, PAM module)
- `/client/` — SSH client code (Go)
- `/server/` — SSH server code (Go)
- `/pam_ssoossh/` — PAM module (C + Go cgo)
- `/internal/` — shared code (Go)
- `/frontend/` — web UI (SvelteKit, TypeScript)
- `/docs/` — documentation
- `/test/` — end-to-end test harness

### Requirements

- Go 1.26+
- Node.js 26+, pnpm 11+
- golangci-lint
- Docker (for `make semgrep`; the e2e suite does not need it)
- libpam headers, only if you touch `pam_ssoossh/`. The devcontainer already
  has them; on a bare host run `scripts/build-env-for-pam.sh`.

`make help` lists every target with a one-line description. Run it first if
you are not sure what exists.

### Running Tests Locally

```bash
# Unit tests, per component, with a coverage summary per component
make test

# Just one package
go test ./server/service/...

# Frontend tests (vitest)
make frontend-test

# Tagged suites that are NOT part of `make test`
make test-resilience     # shutdown, database loss, OIDC loss
make test-load           # load, soak, concurrency (slow; weekly in CI)
make test-migration      # SQLite/Postgres schema parity

# End-to-end. Tier 3 modifies host state: it creates and unlocks a local
# account and runs sshd as root. Read test/e2e/README.md before running it.
make test-e2e
```

Known coverage gaps are tracked in [docs/testing-needs.md](docs/testing-needs.md),
each with the bug that exposed it. Add to it when you find a gap you are not
closing in the same change, and delete entries as they are closed rather than
marking them done.

### Linting

```bash
# Fix everything that can be fixed mechanically. Run this BEFORE `make lint`.
make lint-fix

# Then check. This is the merge gate.
make lint
```

`make lint-fix` matters more than it looks. Several enabled linters are
mechanically fixable — `godot` (comment full stops), the gofmt/goimports
formatters, and the `interface{}` to `any` rewrite — and there is no
pre-commit hook in this repo to catch them. Without `lint-fix` they reach CI
and fail the merge gate over punctuation.

### Code Standards

**Go:**
- Interfaces are defined in the package that implements them, not the package that uses them.
- Errors are wrapped with context: `return fmt.Errorf("operation: %w", err)`.
- Use `context.Context` as the first parameter for I/O operations.
- No global state; pass dependencies through structs.
- slog for structured logging; slog is the only global allowed.
- Imports in 3 groups: standard lib, external, local (separated by blank lines).
- Every function has a comment explaining what it does.
- Tests are colocated: `email.go` → `email_test.go`.
- If code cannot be tested (cgo entry points, unreachable defensive branches), say why at the code in a comment starting `not covered:`.

**TypeScript/Svelte:**
- No `{@html}`, `innerHTML`, or `eval`.
- Svelte's auto-escaping protects against XSS.
- Descriptive test names: "should [action] when [condition]".
- No external mock frameworks; use interfaces to inject test doubles.

**Coverage:**
- Aim for >90% coverage in each package.
- Coverage is reported unfiltered: what `make test`, `make cover`, CI and
  Codecov show is the same number. There is no exclusion list.
- Not all code can be tested (cgo entry points, `crypto/rand` failures,
  defensive branches that cannot be reached). Explain those in place with
  a comment starting `not covered:`, so a reader looking at the coverage
  report finds the reason next to the code. Grep for `not covered:` to see
  every such block.
- That marker is for code a test genuinely cannot reach. Code that is
  merely awkward to reach is a gap: write the test.

## Submitting a Pull Request

1. **Create a branch** off `main`: `git checkout -b feat/your-feature`.
2. **Make your changes.** Write tests as you go.
3. **Run the gate locally:**
   ```bash
   make pre-pr
   ```
4. **Commit with conventional commits.** Keep one concern per commit.
5. **Open a PR** against `main`. The description should explain *why* the change matters, not just *what* changed.
6. **Wait for CI.** See the table below for what blocks a merge.
7. **Respond to feedback.** Add commits (don't amend) so the review history is clear.

### Before you open a PR

`make pre-pr` is the whole checklist in one target. It runs, in order:

| Step | What it does | Why the order matters |
| --- | --- | --- |
| `make fmt` | gofmt plus prettier, in place | Formatting first, so nothing after it reports a formatting problem |
| `make lint-fix` | `golangci-lint run --fix` | Fixes the mechanical findings before anything checks for them |
| `make check-generated` | types, OpenAPI, man pages | Catches a generated file you forgot to regenerate and commit |
| `make ci-required` | every blocking CI check | The actual gate |

Expect the first run to take a while: `ci-required` builds the web UI, runs
the whole unit suite with coverage, builds the PAM module under cgo, and runs
the frontend lint, typecheck, and semgrep scan.

If you want to run one piece at a time, `make ci-required` is the list:

```
fmt-check check-gitignore lint frontend-lint frontend-check actionlint
check-generated build pam test-pam lint-pam cover-ci semgrep
```

**Deliberately not in `pre-pr`:** `test-e2e` (modifies host state, so it stays
opt-in), `test-load` (weekly in CI, not per-PR), and the macOS and Windows
client legs, which need those operating systems. If your change touches
`client/` or `internal/crypto/ssh/agent/`, CI will run those two legs for you
and they are the ones most likely to surprise you — path handling, agent
sockets, and keychain behavior differ per platform.

### What CI blocks on

| Workflow | Blocking | Notes |
| --- | --- | --- |
| `lint` | yes | Go lint, frontend lint and svelte-check, actionlint, .gitignore invariants |
| `codecover` | yes | Unit suite plus the Codecov upload |
| `build` | yes | On PRs: generated-artifact staleness plus a single-target snapshot build. The full signed multi-platform pipeline runs on tags, weekly, and manual dispatch |
| `e2e` | yes | Four tiers. sqlite only except tier 1, which runs both backends |
| `client-matrix` | yes | macOS and Windows client and agent tests |
| `resilience` | yes | Resilience and accessibility. The load job is weekly, not per-PR |
| `security` | partly | semgrep blocks; govulncheck and pnpm audit report to a PR comment |

Most workflows are behind a path filter, so a docs-only PR will show several
checks as skipped. Skipped satisfies branch protection; it is not a failure.

### If CI fails and your local run passed

- **`check-generated`** — you changed a Go wire type, a swag annotation, or a
  cobra command without running `make types`, `make openapi`, or `make gendocs`
  and committing the result.
- **`frontend-check`** — `pnpm build` does not typecheck, so a Svelte or
  TypeScript type error only shows up here. `make frontend-check` locally.
- **`semgrep`** — frontend only, and it is a merge gate rather than advisory.
  `make semgrep` reproduces it exactly (same pinned image, same rule packs).
- **`client-matrix`** — a macOS or Windows path. Nothing local reproduces it;
  read the job log.

### PR Guidelines

- **One concern per PR.** If you're adding a feature, don't also refactor unrelated code.
- **Focused scope.** Aim for PRs that can be reviewed in 10-15 minutes.
- **Avoid merge commits.** Rebase before pushing if CI added new commits.
- **Schema changes are single-owner.** If your PR edits `server/model/` or `server/resources/migrations/`, no other PR can land in parallel until yours is merged. Coordinate with the maintainer first.

## Documentation

- Architecture and design decisions are in `/docs/`.
- The `README.md` in each package (`/client/`, `/server/`, etc.) explains its role.
- For larger features, add a design document to `/docs/` before implementing.

## Reporting Issues

If you find a bug:

1. Check the existing issues to avoid duplicates.
2. Provide:
   - Steps to reproduce
   - Expected vs. actual behavior
   - Your environment (OS, Go version, etc.)
   - Error output or stack traces

## Questions?

- Check the docs in `/docs/`.
- Look at recent PRs and commits to understand patterns.
- Open an issue to discuss architectural questions before coding.

Thank you for contributing!
