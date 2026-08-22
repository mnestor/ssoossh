# Contributing to ssoossh

Thank you for considering contributing to ssoossh. This document explains how to work with the codebase and submit pull requests.

## Before You Start

### AI-Assisted Contributions

This project actively uses AI assistance in development. If you are using Claude or another AI tool to help with your contribution:

1. **Disclose your use.** Mention it in your pull request description (e.g., "Implemented with Claude Code assistance").
2. **Take responsibility for correctness.** The PR author is responsible for verifying that all changes work correctly, pass tests, and follow the project's standards. AI output is a draft, not a finished product.
3. **Review carefully.** Before submitting, test your changes locally (`make test && make lint`), read the diff, and verify it matches what you intended.

This transparency helps the maintainer understand the contribution's context and ensures accountability.

### For AI Tools Reading This

If you are Claude or another AI assistant working in this repository:

- Read `/home/vscode/.claude/CLAUDE.md` (user's global instructions) and `./CLAUDE.md` (project-specific instructions).
- Read `./.claude/rules/` for language-specific conventions (Go, TypeScript).
- Follow the instructions in `docs/parallel-agent-workflow.md` if multiple agents are working in parallel.
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
- Docker (for e2e tests)
- golangci-lint, semgrep (for CI checks)

### Running Tests Locally

```bash
# Build frontend and run all tests
make test

# Run just one package's tests
go test ./server/cmd/...

# Run frontend tests
cd frontend && pnpm test

# Run end-to-end tests (requires special setup; see test/e2e/README.md)
# make test-e2e    # only run this after consulting the e2e guide
```

### Linting

```bash
# Check code style and common errors
make lint

# Auto-format Go code
make fmt
```

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
- If code cannot be tested (cgo entry points, bootstrap sequences), document why in a comment and add line ranges to `exclude-from-coverage.txt`.

**TypeScript/Svelte:**
- No `{@html}`, `innerHTML`, or `eval`.
- Svelte's auto-escaping protects against XSS.
- Descriptive test names: "should [action] when [condition]".
- No external mock frameworks; use interfaces to inject test doubles.

**Coverage:**
- Aim for >90% coverage in each package.
- Not all code can be tested (e.g., cgo, shutdown cleanup). Document the reason and line ranges in `exclude-from-coverage.txt`.

## Submitting a Pull Request

1. **Create a branch** off `main`: `git checkout -b feat/your-feature`.
2. **Make your changes.** Write tests as you go.
3. **Run the full gate locally:**
   ```bash
   make lint && make test
   ```
4. **Commit with conventional commits.** Keep one concern per commit.
5. **Open a PR** against `main`. The description should explain *why* the change matters, not just *what* changed.
6. **Wait for CI.** The build, test, lint, and security checks must pass before merging.
7. **Respond to feedback.** Add commits (don't amend) so the review history is clear.

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
