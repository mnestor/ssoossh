# End-to-end suite

Proves, on every pull request, that `ssoossh ssh login` → browser approval →
certificate → `ssh` actually works. See `docs/dev/e2e-testing-plan.md` for the
full design and why it is built the way it is.

Behind the `e2e` build tag, so `go test ./...` never touches it.

## Running it

```
make test-e2e
```

or directly:

```
go test -tags=e2e ./test/e2e/...
```

Run one tier (or one test) with `-run`:

```
go test -tags=e2e -run TestLogin ./test/e2e/...    # tier 1, wire
go test -tags=e2e -run TestSSH ./test/e2e/...      # tier 3, sshd
go test -tags=e2e -run TestApproval ./test/e2e/... # tier 2, browser
```

Requires a built web UI (`make frontend`) — the harness builds the
`ssoosshd`/`ssoossh` binaries itself on first use, so no separate build step
is needed locally.

## Running two of these at once

Don't — and `make test-e2e` now stops you, by taking an flock on
`/tmp/ssoossh-e2e.lock`. A second run waits instead of interfering.

Worktrees isolate the filesystem, not the host, and this suite reaches the
host in several places: the `ssoossh-e2e` local account, `sshd` under sudo,
and container ports. Those are now individually collision-safe — NATS ports
are allocated rather than hardcoded, and the account creation tolerates
losing the race — but the lock is the belt to those braces, because the
interference used to be silent rather than loud.

`make test-e2e-unlocked` skips the lock if you genuinely want a parallel run.
CI never goes through either target — `e2e.yaml` invokes `gotestsum` on
`./test/e2e/...` directly, and each job has the host to itself.

## What tier 3 does to this machine

`TestSSH_*` (harness/sshd.go) creates a dedicated local account
(`ssoossh-e2e`), unlocks its password slot (`usermod -p '*'` — this does
*not* set a usable password; `sshd_config` still forces
`PasswordAuthentication no`), and runs a real `sshd` as root via `sudo`.
Needs `openssh-server` installed and passwordless `sudo`. It's idempotent —
safe to run repeatedly — but it is a real system-account change, not a
sandboxed one.

There is no PAM tier here. `pam_ssoossh` is a separate project,
[github.com/mnestor/ssoossh-pam](https://github.com/mnestor/ssoossh-pam), and
drives its own PAM stack against a `ssoosshd`. What this suite covers of that
flow is the server side: `cert_options.pam` issuance and narrowing, at tier 1.

## Writing selectors (tier 2)

The harness runs every selector through
`chromedp.WaitVisible(..., chromedp.ByQuery)`, which is `document.querySelector`.

- **Plain CSS only.** `:contains()` is a jQuery extension and `text="..."` is
  Playwright syntax. Neither is valid here; both surface as
  `DOM Error while querying (-32000)`, which does not obviously mean "your
  selector is not CSS".
- **`data-testid` is the convention.** Select on
  `[data-testid="thing"]`, not on classes or DOM shape — Tailwind classes and
  element nesting change when the design does, test ids do not.
- **On a Svelte component, the prop is `testid`, not `data-testid`.** The
  shared components (`Button`, `Card`, `Alert`, `Pager`, `SearchInput`,
  `CertRow`, `PageHeading`, `ServiceCodeRow`) each declare a `testid` prop and
  render `data-testid={testid}` themselves, so `<Card testid="x">` is right and
  `<Card data-testid="x">` sets an unknown prop that Svelte drops at runtime —
  the attribute never reaches the DOM and every selector for it fails. Plain
  HTML elements take `data-testid=` normally, which is what makes it easy to
  miss. `make frontend-check` catches the mistake (`'"data-testid"' does not
  exist in type 'Props'`), so run it before concluding you have a selector bug.

## What `lint-tagged` does and does not prove

`make lint-tagged` typechecks this suite; it does **not** execute it. A green
`lint-tagged` means the e2e tests compile, nothing more. "The e2e tests are
fine" requires an actual `make test-e2e` run — a passing `lint-tagged`
alongside no `test-e2e` run looks exactly the same as a passing suite.

`make lint` and `make test` do not compile this suite at all: it is behind the
`e2e` build tag and neither passes build tags. `lint-tagged` is what compiles
it, and it is part of `make ci-required`, `make pre-pr`, and `make verify`.

## Tier 2 / browser debugging

Tier 2 drives the approval page with chromedp. To watch it run instead of
headless, see `harness/browser.go` for the relevant option (kept as a
one-line toggle since this is genuinely useful while iterating on the
frontend).

## Diagnostics

On failure, each tier writes what it can to
`test/e2e/_artifacts/<test-name>/`: the `ssoosshd` log, the `sshd` log, the
client's captured stdout/stderr, and (tier 2) a screenshot plus the browser
console. CI uploads this directory as a build artifact when the job fails.

## Flakes

No automatic retries — a flaky test gets quarantined with an issue, not
`-count` tricks in CI. If you hit one locally, `go test -tags=e2e -run
<Test> -count=20` is the fastest way to characterize it before filing.
