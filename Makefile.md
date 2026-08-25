# The Makefile

Every target, who runs it, and what it needs. `make help` prints the same list
with one-liners; this file carries the parts a one-liner cannot.

For the contribution *process* — what to run before a PR, in what order — see
[CONTRIBUTING.md](CONTRIBUTING.md). This is the reference.

## Start here

| Target | What it does | Who / when |
| --- | --- | --- |
| `make help` | Lists every target (default goal) | Anyone, first |
| `make verify` | Compiles and typechecks everything, runs unit tests. No containers, no host changes | Everyone, in the edit loop |
| `make pre-pr` | Formats, autofixes, then runs every merge gate | Everyone, before opening a PR |

`pre-pr` is `fmt` → `lint-fix` → `check-generated` → `ci-required`, in that
order: the autofix steps must run before anything checks for what they fix.
**Do not hand-assemble a subset in its place** — see [the traps](#four-things-hide-code-from-the-obvious-command).

## Build

| Target | What it does | Who / when | Requirements |
| --- | --- | --- | --- |
| `frontend` | Builds the web UI into `server/frontend/dist` | Rarely by hand — it's a prerequisite | Node, pnpm |
| `build` | `go build ./...` | Edit loop; in `ci-required` | cgo, frontend dist |
| `pam` | Builds `pam_ssoossh.so` into `.build/` | Touching `pam_ssoossh/`; in `ci-required` | cgo, libpam headers |
| `linux` | Snapshot build, linux/amd64 only | Checking a real binary | goreleaser |
| `binaries` | Snapshot build, every release target | Release rehearsal | goreleaser |
| `all` | `frontend` + `binaries` | Release rehearsal | both |
| `frontend-clean` | Removes the built UI | Forcing a clean rebuild | — |

The bundle is a real file target (`server/frontend/dist/index.html`) with the
UI sources as prerequisites, so dependents rebuild when the UI changes.
Compiling does **not** need it — the tracked `dist/.gitignore` satisfies the
`//go:embed` pattern. Serving or testing a UI does.

## Test

| Target | What it does | Who / when | Requirements |
| --- | --- | --- | --- |
| `test` | Unit tests per component, coverage summary each | Edit loop | cgo, frontend dist |
| `test-server` `test-client` `test-pam` `test-internal` | One component | Narrowing a failure | as above |
| `test-race` | Unit tests under `-race` | Chasing a data race | cgo (race requires it) |
| `cover` | Coverage HTML at `.coverage/coverage.html` | Inspecting your own gaps | cgo, frontend dist |
| `cover-ci` | Coverage exactly as `codecover.yaml` runs it | In `ci-required` | cgo, frontend dist |
| `cover-floors` | Fails if a package fell below `.coverage-floors` | In `ci-required` | cgo, frontend dist |
| `frontend-test` | vitest unit and a11y tests | Touching the frontend; in `ci-required` | Node, pnpm |

`cover-ci` is two runs, not one: every file in `pam_ssoossh/` is behind
`//go:build pam`, so a plain `./...` compiles that package to nothing and PAM
contributes neither numerator nor denominator.

`cover-floors` ratchets **per package**, not on one module number — a module
total moves for unrelated reasons and hides exactly the regression it exists
to catch. Given no argument it runs its own coverage pass (both runs,
including PAM); CI hands it the already-combined profile instead. Floors are
a ratchet: raising one is routine, lowering one is a deliberate edit to
`.coverage-floors` that shows up in review.

### Tagged suites — none run as part of `make test`

| Target | Tag | Who / when | Requirements |
| --- | --- | --- | --- |
| `test-e2e` | `e2e` | Opt-in. Touching auth, the login flow, or the harness | **Modifies host state** — read [test/e2e/README.md](test/e2e/README.md) first |
| `test-resilience` | `resilience` | Touching shutdown, DB, or OIDC handling | frontend dist |
| `test-load` | `load` | Rarely — weekly in CI | frontend dist; slow |
| `test-migration` | `dbparity` | Changing a migration | — |
| `test-hsm` | `softhsm` | Touching the HSM key source | softhsm2, opensc |
| `test-memory-leak` | `memory_leak_test` | Quarantined | These assert defects **exist** — they are meant to fail |

`test-e2e` tier 3 creates and unlocks a local account and runs sshd as root
via sudo. That is why it is deliberately absent from `ci-required`.

## Format and lint

| Target | What it does | Who / when | Requirements |
| --- | --- | --- | --- |
| `fmt` | gofmt + prettier, in place | First step of `pre-pr` | Node, pnpm |
| `fmt-check` | Fails if any Go file is not gofmt-clean | In `ci-required` | — |
| `lint-fix` | Every finding golangci-lint can fix | **Before `lint`**, always | cgo |
| `lint` | golangci-lint over the module | Merge gate | cgo |
| `lint-tagged` | golangci-lint over the build-tagged suites | Merge gate; in `verify` | cgo |
| `lint-cross` | golangci-lint over the Windows and macOS builds | Merge gate; in `verify` | cgo **off** |
| `lint-server` `lint-client` `lint-pam` `lint-internal` | One component | Narrowing a failure | cgo (server, pam) |
| `frontend-lint` | prettier `--check` + eslint | Touching the frontend | Node, pnpm |
| `frontend-check` | svelte-check — the only thing that typechecks hand-written Svelte | Touching the frontend | Node, pnpm |
| `actionlint` | Lints the workflow files | Touching `.github/workflows/` | actionlint or `go run` |
| `check-gitignore` | Asserts the `.gitignore` invariants | Touching a `.gitignore` | — |

`lint-fix` matters more than it looks: several enabled linters are
mechanically fixable (`godot`, the formatters, `interface{}` → `any`) and
there is no pre-commit hook here. Skipping it fails the merge gate over
punctuation.

## Generated artifacts

`make check-generated` runs all four gates. Each regenerates into the tree and
asserts nothing changed. **Edit the source, never the generated file.**

| Regenerate with | Gated by | Source of truth |
| --- | --- | --- |
| `types` | `types-check` | Go wire structs (see `tygo.yaml`) |
| `openapi` | `openapi-check`, `openapi-lint` | swag annotations on `server/controller` handlers |
| `gendocs` | `man-check` | the cobra command trees |
| `third-party-licenses` | *(none — not committed)* | the Go module cache |

The checks compare sha256 hashes rather than `git diff`, deliberately:
`git diff` ignores untracked files, so a never-committed generated file would
pass while reporting nothing.

## Security

| Target | Merge gate? | Who / when | Requirements |
| --- | --- | --- | --- |
| `semgrep` | **Yes** | In `ci-required` | Docker |
| `govulncheck` | Advisory | Dependency bumps | govulncheck on PATH |
| `pnpm-audit` | Advisory | Dependency bumps | Node, pnpm |
| `security` | — | All three at once | all of the above |

Advisory = CI reports to the PR summary rather than blocking, because either
scanner can surface a dependency you cannot fix in the same PR.

`semgrep` is frontend-only on purpose: gosec inside golangci-lint covers the
Go side, so pointing semgrep there would re-report the same findings twice.

## CI mirrors

| Target | What it is | Who / when |
| --- | --- | --- |
| `ci-required` | Every check that can fail a PR | Via `pre-pr` |
| `ci-advisory` | The two non-blocking scanners (prefixed `-`, so they never fail the run) | Dependency work |
| `ci` | Both | Full local rehearsal |

Deliberately **not** in `ci-required`: `test-e2e` (modifies host state),
`test-load` (weekly), and the client-matrix macOS/Windows legs (need those OSes).

## Misc

| Target | What it does | Who / when |
| --- | --- | --- |
| `version` | Prints the version this tree would build as | Debugging a stamp |
| `update` | `go get -u ./...` + `go mod tidy` | Dependency bumps |
| `update-go-version` | Prints the two files you must edit by hand | Go toolchain bumps |

## Four things hide code from the obvious command

Why `verify` and `pre-pr` are targets rather than habits. Each of these lets a
plain command report success over code it never looked at:

| What hides | Hidden from | How it bit | Covered by |
| --- | --- | --- | --- |
| **Build tags** | `lint` passes none; `test` builds none | A suite behind `e2e`, `resilience`, `load`, `dbparity`, `softhsm`, or `natsintegration` can fail to compile outright while both report success | `lint-tagged` |
| **GOOS** | `lint` runs with the host's | A G115 overflow bug sat in `client/config/policy_windows.go`, on a value an admin sets through Group Policy | `lint-cross` |
| **cgo** | golangci-lint can't see into a cgo file | Every symbol referenced only from one reports as unused. The devcontainer defaults `CGO_ENABLED=0` | Explicit `CGO_ENABLED=1` per recipe |
| **The `pam` tag** | `./...` and coverage both | An entire package contributes nothing, silently | `test-pam`, `lint-pam`, `cover-ci`'s second run |

## Cross-cutting requirements

| Requirement | Needed by | Why |
| --- | --- | --- |
| **cgo on** | `build`, `test-server`, `test-internal`, `test-race`, `cover*`, `lint`, `lint-pam`, `pam`, `gendocs`, tagged suites | `server/signer`'s HSM key source reaches libpkcs11 through crypto11; without it the server packages do not build at all. Set per-recipe to keep the rest of the tree cgo-free |
| **cgo off** | `lint-cross` only | There is no cross-compiling cgo toolchain — which is also why its scope is a package list, not `./...` |
| **Frontend bundle** | Anything embedding or testing the UI (declared as a prerequisite) | `server/frontend` embeds it. A stale bundle makes a browser test assert against whenever the UI was last built — presenting as a selector timeout, indistinguishable from a wrong selector |
| **Docker** | `semgrep` only | Pinned image, same as CI |
| **libpam headers** | `pam`, `test-pam`, `lint-pam` | The devcontainer has them; on a bare host run `scripts/build-env-for-pam.sh` |
| **Host state + sudo** | `test-e2e` tier 3 | Creates and unlocks a local account, runs sshd as root |
| **softhsm2 + opensc** | `test-hsm` | A real PKCS#11 token |

`ACTIONLINT` and `REDOCLY` prefer a binary on PATH (the build image installs
pinned ones) and fall back to `go run` / `npx` at a pinned version. Keep those
pins in step with `.github/docker/Dockerfile.runner`.

## Variables and environment

| Thing | Effect |
| --- | --- |
| `-include .env`, `.env.local` | Optional local overrides, silently skipped if absent |
| `.EXPORT_ALL_VARIABLES` | Puts every variable — those files included — into every recipe's environment. How a local `NFPM_PASSPHRASE` or `QUILL_*` reaches goreleaser without any recipe naming it. Wide on purpose; removing it would silently break a local signed build |
| `VERSION` | From `GITHUB_REF` in CI, `git describe` otherwise |
| `LDFLAGS` | Stamps version, commit, date, and builder into the binary |
| `DIRTY` | Whether the tree has uncommitted changes; drives goreleaser's snapshot mode |

Overridable with `?=`: `LINT_CROSS_PACKAGES`, `LINT_CROSS_GOOS`, `ACTIONLINT`,
`REDOCLY`.
