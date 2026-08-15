# End-to-end suite

Proves, on every pull request, that `ssoossh ssh login` → browser approval →
certificate → `ssh` actually works. See `docs/e2e-testing-plan.md` for the
full design and `docs/release-phase2-e2e.md` for why it's built the way it
is.

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
go test -tags=e2e -run TestLogin ./test/e2e/...   # tier 1, wire
go test -tags=e2e -run TestSSH ./test/e2e/...      # tier 3, sshd
go test -tags=e2e -run TestApproval ./test/e2e/... # tier 2, browser
```

Requires a built web UI (`make frontend`) — the harness builds the
`ssoosshd`/`ssoossh` binaries itself on first use, so no separate build step
is needed locally.

## What tier 3 does to this machine

`TestSSH_*` (harness/sshd.go) creates a dedicated local account
(`ssoossh-e2e`), unlocks its password slot (`usermod -p '*'` — this does
*not* set a usable password; `sshd_config` still forces
`PasswordAuthentication no`), and runs a real `sshd` as root via `sudo`.
Needs `openssh-server` installed and passwordless `sudo`. It's idempotent —
safe to run repeatedly — but it is a real system-account change, not a
sandboxed one.

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
