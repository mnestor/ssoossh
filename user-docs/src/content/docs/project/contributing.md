---
title: Contributing
description: How to work with the ssoossh codebase, what to run before a PR, and what CI blocks on.
eyebrow: Project
sidebar:
  order: 4
---

How to work with the codebase and submit pull requests. The authoritative
copies live in the repository:
[CONTRIBUTING.md](https://github.com/mnestor/ssoossh/blob/main/CONTRIBUTING.md)
for the process and
[Makefile.md](https://github.com/mnestor/ssoossh/blob/main/Makefile.md) for
the target reference.

## Before you start

### AI-assisted contributions

This project actively uses AI assistance in development. If you are using
Claude or another AI tool to help with your contribution:

1. **Disclose your use.** Mention it in your pull request description (for
   example, "Implemented with Claude Code assistance").
2. **Take responsibility for correctness.** The PR author is responsible for
   verifying that all changes work correctly, pass tests, and follow the
   project's standards. AI output is a draft, not a finished product.
3. **Review carefully.** Before submitting, run `make pre-pr`, read the diff,
   and verify it matches what you intended.

An AI assistant working in the repository should read `./CLAUDE.md` for
project-specific instructions, `./.claude/rules/` for the Go and TypeScript
conventions, and `AGENTS.md` when several agents work in parallel. The project
uses `rtk` to minimize token usage in bash commands.

### Requirements

- Go 1.26+
- Node.js 26+, pnpm 11+
- golangci-lint
- Docker, for `make semgrep`. The e2e suite does not need it.

The `pam_ssoossh` PAM module is a separate project,
[mnestor/ssoossh-pam](https://github.com/mnestor/ssoossh-pam); its build
toolchain is not needed here. Its documentation is still maintained in this
repository under `user-docs/`.

`make help` lists every target with a one-line description. Run it first if
you are not sure what exists.

### Layout

| Path | Contents |
| --- | --- |
| `/cmd/` | binary entrypoints (client, server, PAM module) |
| `/client/` | SSH client code (Go) |
| `/server/` | server code (Go) |
| `/internal/` | shared code (Go) |
| `/frontend/` | web UI (SvelteKit, TypeScript) |
| `/docs/` | documentation |
| `/user-docs/` | this documentation site (Astro Starlight) |
| `/test/` | end-to-end test harness |

## Branch naming and commits

Branches follow the pattern `feat/`, `fix/`, or `chore/` followed by a short
description:

```bash
git checkout -b feat/add-token-refresh
```

Commits use [Conventional Commits](https://www.conventionalcommits.org/):

```text
feat(client): add token refresh on certificate expiry

Tokens are now automatically refreshed 10 seconds before
expiry, reducing the likelihood of failed operations.

Fixes #42
```

## The three targets to know

| Target | When |
| --- | --- |
| `make help` | First. Lists every target (it is the default goal) |
| `make verify` | In the edit loop. Compiles and typechecks everything, runs unit tests. No containers, no host changes |
| `make pre-pr` | Before opening a PR. Formats, autofixes, then runs every merge gate |

`pre-pr` is `fmt` then `lint-fix` then `check-generated` then `ci-required`,
in that order: the autofix steps must run before anything checks for what they
fix.

:::caution[Do not hand-assemble a subset in place of `pre-pr`]
`make lint` passes no build tags and `make test` does not build the tagged
suites, so a test behind `e2e`, `resilience`, `load`, `dbparity`, `softhsm` or
`natsintegration` can fail to compile entirely while `lint`, `test`,
`check-generated` and every frontend gate report success. `make lint` also
runs with your own `GOOS`, so nothing in that list sees the Windows or macOS
build. `lint-tagged` and `lint-cross` close those gaps, and they are the two a
hand-assembled list leaves out.
:::

`make verify` is the deliberate subset that keeps both in: `lint`,
`lint-tagged`, `lint-cross`, `test`, `check-generated`. It is faster than
`pre-pr`, still compiles the tagged suites, and still typechecks the Windows
and macOS builds. It is not a merge gate: `pre-pr` is.

## Make targets

The curated set `make help` prints. Per-component variants (`test-server`,
`test-client`, `test-internal`, and the matching `lint-*`) and the
internal check halves (`types-check`, `man-check`, and friends, all reached
through `check-generated`) are deliberately left out of both.

### Before you open a PR

| Target | What it does |
| --- | --- |
| `make pre-pr` | Format, autofix, then run every merge gate CI runs |
| `make verify` | Fast subset for the edit loop -- still compiles the tagged suites |
| `make worktree` | Create a ready-to-use agent worktree: `make worktree NAME=<name> [BASE=main] [WORKTREE_BRANCH=...]` |

### Build

| Target | What it does |
| --- | --- |
| `make frontend` | Build the web UI into `server/frontend/dist` |
| `make build` | Build all Go packages |
| `make linux` | Snapshot build for linux/amd64 only |
| `make binaries` | Snapshot build for every release target |
| `make server-linux-build-local` | Build `ssoosshd` for a local `docker build` |
| `make frontend-clean` | Remove the built web UI |

### Test

| Target | What it does |
| --- | --- |
| `make test` | Unit tests per component, with coverage |
| `make test-race` | Unit tests under `-race` |
| `make cover` | Coverage HTML report at `.coverage/coverage.html` |
| `make cover-ci` | Coverage exactly as `codecover.yaml` runs it |
| `make cover-floors` | Fail if any package dropped below its floor in `.coverage-floors` |
| `make frontend-test` | Frontend unit and a11y tests (vitest) |

### Test: tagged suites, not part of `make test`

| Target | What it does |
| --- | --- |
| `make test-hsm` | HSM key source tests against softhsm2 (needs softhsm2 + opensc) |
| `make test-e2e` | End-to-end suite (modifies host state) |
| `make test-e2e-unlocked` | `test-e2e` without the serialising lock, for a deliberate parallel run |
| `make test-memory-leak` | Memory leak repro tests |
| `make test-resilience` | Resilience suite (shutdown, db loss, oidc loss) |
| `make test-migration` | SQLite/Postgres migration parity checks |
| `make test-load` | Load, soak, and concurrency suite (slow) |

Each needs its tag: `e2e`, `resilience`, `load`, `dbparity`, `softhsm`,
`memory_leak_test`. `test-memory-leak` is quarantined: those tests assert
defects **exist**, so they are meant to fail.

:::danger[`test-e2e` tier 3 modifies host state]
It creates and unlocks a local account and runs `sshd` as root via sudo. Read
[test/e2e/README.md](https://github.com/mnestor/ssoossh/blob/main/test/e2e/README.md)
before running it. That is why it is deliberately absent from `ci-required`.
:::

### Format and lint

| Target | What it does |
| --- | --- |
| `make fmt` | Format Go and frontend sources in place |
| `make fmt-check` | Fail if any Go file is not gofmt-clean |
| `make lint-fix` | Auto-fix every lint finding golangci-lint can fix |
| `make lint` | golangci-lint over the whole module (merge gate) |
| `make lint-tagged` | golangci-lint over the build-tagged suites `lint` cannot see |
| `make lint-cross` | golangci-lint the Windows and macOS builds `lint` cannot see |
| `make frontend-lint` | `prettier --check` and eslint over the frontend |
| `make frontend-check` | `svelte-check` the frontend against `tsconfig.json` |
| `make actionlint` | Lint the GitHub Actions workflow files |
| `make check-gitignore` | Assert the `.gitignore` invariants hold |

`lint-fix` matters more than it looks, and must run **before** `lint`: several
enabled linters are mechanically fixable (`godot` comment full stops, the
gofmt/goimports formatters, the `interface{}` to `any` rewrite) and there is
no pre-commit hook in this repo. Skipping it fails the merge gate over
punctuation. `frontend-check` is the only thing that typechecks hand-written
Svelte.

### Generated artifacts

| Target | What it does |
| --- | --- |
| `make check-generated` | Assert every generated artifact is current |
| `make types` | Regenerate frontend wire types from Go structs |
| `make openapi` | Regenerate `docs/openapi.yaml` from swag annotations |
| `make gendocs` | Regenerate man pages from the cobra commands |
| `make confdocs` | Regenerate the config reference and `defaults.yaml` comments |
| `make makefile-docs` | Regenerate the target tables in `Makefile.md` |
| `make third-party-licenses` | Regenerate `THIRD-PARTY-LICENSES.md` |

**Edit the source, never the generated file.** Each gate regenerates into the
tree and asserts nothing changed.

| Regenerate with | Gated by | Source of truth |
| --- | --- | --- |
| `types` | `types-check` | Go wire structs (see `tygo.yaml`) |
| `openapi` | `openapi-check`, `openapi-lint` | swag annotations on `server/controller` handlers |
| `gendocs` | `man-check` | the cobra command trees |
| `confdocs` | `confdocs-check` | the doc comments on `server/config`'s structs |
| `makefile-docs` | `makefile-docs-check` | the `##@`/`##` annotations in the Makefile |
| `third-party-licenses` | *(none, not committed)* | the Go module cache |

`confdocs` produces two files from one walk: the OPTIONS body of
`docs/man/ssoosshd.yaml.5`, and the comments in `server/config/defaults.yaml`,
the file embedded in the binary and installed as
`/etc/ssoossh/ssoosshd.yaml`. Only the prose is generated. The **values** in
`defaults.yaml` are read from the file and written back untouched, because
that is the file viper loads; `server/config`'s golden test guards them.
Adding a config field with no doc comment fails the generator rather than
shipping a bare key name in two places.

The checks compare sha256 hashes rather than `git diff`, deliberately:
`git diff` ignores untracked files, so a never-committed generated file would
pass while reporting nothing.

`gendocs` covers the `.1` and `.8` pages generated from cobra. The `.5`
config-format pages (`ssoossh.yaml.5`, `ssoosshd.yaml.5`) are hand-written
and outside that set.

### Security and CI mirrors

| Target | What it does |
| --- | --- |
| `make govulncheck` | Scan Go dependencies for known vulnerabilities |
| `make pnpm-audit` | Audit frontend dependencies |
| `make semgrep` | Semgrep scan of the frontend (merge gate) |
| `make security` | Run every security scanner |
| `make ci-required` | Every blocking check CI runs |
| `make ci-advisory` | The non-blocking scanners, run the way CI runs them |
| `make ci` | `ci-required` plus the advisory scanners |
| `make version` | Print the version this tree would build as |
| `make update` | Update Go dependencies and tidy |

`semgrep` is the only merge gate of the three scanners; `govulncheck` and
`pnpm-audit` are advisory, because either can surface a dependency you cannot
fix in the same PR. `semgrep` is frontend-only on purpose: gosec inside
golangci-lint covers the Go side. Details:
[dependency scanning](/ssoossh/project/dependency-scanning/).

Deliberately **not** in `ci-required`: `test-e2e` (modifies host state),
`test-load` (weekly), and the client-matrix macOS/Windows legs (they need
those operating systems).

### Four things hide code from the obvious command

Why `verify` and `pre-pr` are targets rather than habits. Each of these lets a
plain command report success over code it never looked at.

| What hides | Hidden from | How it bit | Covered by |
| --- | --- | --- | --- |
| **Build tags** | `lint` passes none; `test` builds none | A suite behind `e2e`, `resilience`, `load`, `dbparity`, `softhsm`, or `natsintegration` can fail to compile outright while both report success | `lint-tagged` |
| **GOOS** | `lint` runs with the host's | A G115 overflow bug sat in `client/config/policy_windows.go`, on a value an admin sets through Group Policy | `lint-cross` |
| **cgo** | golangci-lint cannot see into a cgo file | Every symbol referenced only from one reports as unused. The devcontainer defaults `CGO_ENABLED=0` | Explicit `CGO_ENABLED=1` per recipe |

`cover-floors` ratchets **per package**, not on one module number, because a module total moves for
unrelated reasons and hides exactly the regression it exists to catch. Raising
a floor is routine; lowering one is a deliberate edit to `.coverage-floors`
that shows up in review.

## Code standards

**Go**

- Interfaces are defined in the package that implements them, not the package
  that uses them.
- Errors are wrapped with context: `return fmt.Errorf("operation: %w", err)`.
- `context.Context` is the first parameter for I/O operations.
- No global state; pass dependencies through structs.
- No `init()` functions; wire startup explicitly. In tests that need
  package-wide setup, use `TestMain`.
- slog for structured logging; slog is the only global allowed.
- Imports in three groups: standard lib, external, local, separated by blank
  lines.
- Every function has a comment explaining what it does.
- Tests are colocated: `email.go` becomes `email_test.go`.

**TypeScript/Svelte**

- No `{@html}`, `innerHTML`, or `eval`. Svelte's auto-escaping protects
  against XSS.
- Descriptive test names: "should [action] when [condition]".
- No external mock frameworks; use interfaces to inject test doubles.

**Coverage**

- Aim for over 90% coverage in each package.
- Coverage is reported unfiltered: what `make test`, `make cover`, CI and
  Codecov show is the same number. There is no exclusion list.
- Not all code can be tested (cgo entry points, `crypto/rand` failures,
  defensive branches that cannot be reached). Explain those in place with a
  comment starting `not covered:`, so a reader looking at the coverage report
  finds the reason next to the code. Grep for `not covered:` to see every such
  block.
- That marker is for code a test genuinely cannot reach. Code that is merely
  awkward to reach is a gap: write the test.

Known coverage gaps are tracked in
[docs/dev/testing-needs.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/testing-needs.md),
each with the bug that exposed it. Add to it when you find a gap you are not
closing in the same change, and delete entries as they are closed rather than
marking them done.

## Submitting a pull request

1. **Create a branch** off `main`: `git checkout -b feat/your-feature`.
2. **Make your changes.** Write tests as you go.
3. **Run the gate locally:** `make pre-pr`.
4. **Commit with conventional commits.** Keep one concern per commit.
5. **Open a PR** against `main`. The description should explain *why* the
   change matters, not just *what* changed.
6. **Wait for CI.**
7. **Respond to feedback.** Add commits rather than amending, so the review
   history is clear.

Expect the first `pre-pr` run to take a while: `ci-required` builds the web
UI, runs the whole unit suite with coverage, and runs the frontend lint,
typecheck, and semgrep scan.

### What CI blocks on

| Workflow | Blocking | Notes |
| --- | --- | --- |
| `lint` | yes | Go lint: the host build and the Windows and macOS builds. Plus frontend lint and svelte-check, actionlint, `.gitignore` invariants |
| `codecover` | yes | Unit suite plus the Codecov upload |
| `build` | yes | On PRs: generated-artifact staleness plus a single-target snapshot build. The full signed multi-platform pipeline runs on tags, weekly, and manual dispatch |
| `e2e` | yes | Three tiers plus the multi-signer job. sqlite only except tier 1, which runs both backends |
| `client-matrix` | yes | macOS and Windows client and agent tests |
| `resilience` | yes | Resilience and accessibility. The load job is weekly, not per-PR |
| `security` | partly | semgrep blocks; govulncheck and pnpm audit report to a PR comment |

Most workflows are behind a path filter, so a docs-only PR will show several
checks as skipped. Skipped satisfies branch protection; it is not a failure.

### If CI fails and your local run passed

- **`check-generated`** -- you changed a Go wire type, a swag annotation, or a
  cobra command without running `make types`, `make openapi`, or
  `make gendocs` and committing the result.
- **`frontend-check`** -- `pnpm build` does not typecheck, so a Svelte or
  TypeScript type error only shows up here. Run `make frontend-check` locally.
- **`semgrep`** -- frontend only, and a merge gate rather than advisory.
  `make semgrep` reproduces it exactly (same pinned image, same rule packs).
- **`client-matrix`** -- a macOS or Windows path. Nothing local reproduces it;
  read the job log. If your change touches `client/` or
  `internal/crypto/ssh/agent/`, CI runs those legs for you and they are the
  ones most likely to surprise you: path handling, agent sockets, file modes,
  and keychain behavior all differ per platform. See
  [cross-platform-testing.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/cross-platform-testing.md)
  for the differences that have actually broken a build.

### PR guidelines

- **One concern per PR.** If you are adding a feature, do not also refactor
  unrelated code.
- **Focused scope.** Aim for PRs that can be reviewed in 10 to 15 minutes.
- **Avoid merge commits.** Rebase before pushing if CI added new commits.
- **Schema changes are single-owner.** If your PR edits `server/model/` or
  `server/resources/migrations/`, no other PR can land in parallel until yours
  is merged. Coordinate with the maintainer first.

## Cross-cutting build requirements

| Requirement | Needed by | Why |
| --- | --- | --- |
| **cgo on** | `build`, `test-server`, `test-internal`, `test-race`, `cover*`, `lint`, `gendocs`, tagged suites | `server/signer`'s HSM key source reaches libpkcs11 through crypto11; without it the server packages do not build at all. Set per-recipe to keep the rest of the tree cgo-free |
| **cgo off** | `lint-cross` only | There is no cross-compiling cgo toolchain, which is also why its scope is a package list rather than `./...` |
| **Node, pnpm** | `frontend`, `frontend-*`, `fmt`, `pnpm-audit` | The web UI toolchain |
| **Frontend bundle** | Anything embedding or testing the UI | `server/frontend` embeds it. A stale bundle makes a browser test assert against whenever the UI was last built, presenting as a selector timeout indistinguishable from a wrong selector |
| **Docker** | `semgrep` only | Pinned image, same as CI |
| **Host state + sudo** | `test-e2e` tier 3 | Creates and unlocks a local account, runs sshd as root |
| **softhsm2 + opensc** | `test-hsm` | A real PKCS#11 token |
| **goreleaser** | `linux`, `binaries`, `all` | Snapshot builds |

## Developer documentation

Testing plans and design records live in the repository under `docs/dev/`.

| Document | What it covers |
| --- | --- |
| [e2e-testing-plan.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/e2e-testing-plan.md) | The end-to-end merge gate: login, browser approval, certificate, `ssh` |
| [cross-platform-testing.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/cross-platform-testing.md) | Testing the client across macOS, Linux, and Windows |
| [testing-strategy-assessment.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/testing-strategy-assessment.md) | What further test investment would and would not buy |
| [test-coverage-gap-map.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/test-coverage-gap-map.md) | Where coverage is thin and why |
| [testing-needs.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/testing-needs.md) | Known coverage gaps, each with the evidence it is real. A worklist |
| [mutation-testing-findings.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/mutation-testing-findings.md) | What mutation testing surfaced |
| [multi-instance-safety-plan.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/multi-instance-safety-plan.md) | Design record for multi-instance safety (implemented) |
| [signer-split-deferred.md](https://github.com/mnestor/ssoossh/blob/main/docs/dev/signer-split-deferred.md) | Design record for the split signer process (implemented) |

Architecture and design records for the shipped system are on this site under
[Internals](/ssoossh/internals/architecture/). For larger features, add a
design document to `docs/proposals/` before implementing.

## This documentation site

The site is an Astro Starlight project in `user-docs/`. Pages are Markdown
under `user-docs/src/content/docs/`, and the site is served under a `/ssoossh`
base path, so internal links are written as absolute paths with that prefix
and a trailing slash.

```bash
cd user-docs
npm install
npm run dev     # local preview with hot reload
npm run build   # what CI builds
```

The [configuration reference](/ssoossh/reference/config/) and the CLI
reference are **generated**. Do not edit them by hand:

- `make confdocs` regenerates the `reference/config/` pages (and the sidebar
  entries that order them) from the doc comments on `server/config`'s structs,
  alongside the man page and the annotated `defaults.yaml`.
- `make gendocs` regenerates the man pages from the cobra command trees, which
  the CLI reference follows.
- `make openapi` regenerates `docs/openapi.yaml`, which backs the
  [HTTP API reference](/ssoossh/reference/api/).

## Reporting issues

If you find a bug, check the existing issues to avoid duplicates, then
provide: steps to reproduce, expected versus actual behavior, your
environment (OS, Go version), and the error output or stack trace. Open an
issue to discuss architectural questions before coding.
