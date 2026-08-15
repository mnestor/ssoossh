# End-to-end testing plan

**Status: planned, and being implemented — see
[release-phase2-e2e.md](release-phase2-e2e.md) for the delivery order.**
Nothing here is implemented yet. It is written from a full manual run of the
loop performed during the (now-removed) web UI and client delivery phases, so
the obstacles below are ones that were actually hit rather than anticipated.

## Goal

Prove, on every pull request, that the product works: `ssoossh ssh login` →
a human approves in a browser → a certificate arrives → `ssh` authenticates
with it. That sentence is the whole product, and today nothing in CI checks
any of it.

The gap is not small. The unit and component suites are healthy — 109 frontend
tests, a full Go pipeline test — but every one of them mocks the thing on the
other side of the wire. Between them sit failures none can see:

- The approval page fetches an endpoint the server does not serve, or serves
  under a different path (this was the state of the old frontend: every
  authenticated route infinite-redirected to a 404, with all tests green).
- The CSP the server sends blocks the SPA's own JavaScript. Only a real
  browser against the real server can catch this.
- The session cookie or the CSRF origin check rejects the browser's approve
  POST — both derive from `http.public_url`, which no unit test exercises.
- The certificate is issued correctly and `sshd` still refuses it, because of
  an extension, principal, or CA-format problem no Go assertion would notice.

The manual run found two config-documentation bugs and confirmed one hard
constraint (options are trimmed, not rejected, and shown before approval) that
is otherwise only asserted against fixtures.

## Shape

One Go test binary that owns every process it needs, behind a build tag:

```
go test -tags=e2e ./test/e2e/...
```

Tagged rather than skipped, so `go test ./...` stays fast and hermetic and the
cross-phase gate keeps its meaning.

**Docker is available and is needed anyway** — the PAM build (Phase 7) and the
release rehearsal (Phase 6) both require containers, so the harness's
no-Docker design is not a claim that Docker is unavailable. It is a claim
about the *merge gate*: a container stack with a real identity provider is
minutes slower and has more moving parts than a per-PR check should, and the
harness answers "did this change break issuance?" in about three minutes. The
compose stack answers a different question — "does a fresh install work
against real pocket-id?" — and belongs to the release rehearsal.


### Layout

```
test/e2e/
  harness/
    idp.go        # OIDC provider on an httptest.Server
    server.go     # ssoosshd: config, start, wait for /healthz, teardown
    agent.go      # a private ssh-agent per test
    sshd.go       # a real sshd trusting the test CA
    client.go     # runs the ssoossh binary, captures stdout/stderr
    browser.go    # drives the approval page
  login_test.go     # tier 1: the wire
  approval_test.go  # tier 2: the browser
  ssh_test.go       # tier 3: sshd
```

`test/` is the standard Go location for external test apparatus, and keeps
this out of `internal/`, which is shared *product* code.

### The identity provider

An `httptest.Server` inside the harness — discovery, an authorization endpoint
with a real form, code exchange, and a JWKS, signing RS256 ID tokens with a key
generated per run. Roughly 200 lines, no dependencies beyond the standard
library and `golang.org/x/crypto`.

This is deliberately a real provider rather than a stub inside `ssoosshd`:
`go-oidc` performs discovery, validates the token against the JWKS, and checks
the nonce, so the server's actual authentication path runs. A stub would prove
nothing about the code that will face pocket-id.

`httptest.Server` also solves port allocation for free, which matters — see
"Ports" below.

### Tiers

Three, in one job, cheapest first so a break is reported at the layer it
happened.

| Tier | What runs | Catches | Budget |
| --- | --- | --- | --- |
| 1 — wire | Harness + `ssoossh` binary; approval driven over HTTP with a cookie jar walking the OIDC redirects | Request lifecycle, SSE delivery, narrowing, session and CSRF handling | ~10s |
| 2 — browser | The same, with the approval page driven in a real browser | The SPA against the real server: routing, CSP, cookies, the granted-vs-requested rendering | ~30s |
| 3 — ssh | Tier 1 plus a real `sshd` trusting the test CA | The certificate actually authenticating a session | ~15s |

Tier 1 is worth having separately from tier 2 despite the overlap: when both
fail, the pair says whether the break is in the server or the UI, which is the
first question anyone asks.

## What it asserts

Mapped to the delivery plan's exit criteria, so a green run means something
specific.

| Assertion | Tier | Criterion |
| --- | --- | --- |
| `ssh login` prints the approval URL *while still waiting* | 1 | Phase 5, the API reshape |
| Approving delivers a certificate over SSE | 1 | Phase 4 |
| The certificate carries only server-permitted extensions, and no critical options | 1 | Hard constraint: server config is the outer bound |
| Denying resolves the waiting client with `denied`, no certificate | 1 | Phase 4 |
| A second `login` reuses the valid certificate without a new approval | 1 | Phase 5, item 3 |
| `logout` removes ssoossh's certificate and leaves an unrelated key untouched | 1 | Phase 5, item 4 |
| An unauthenticated visitor is offered sign-in, not an approve button | 2 | Phase 4 |
| The page shows trimmed options struck through before approval | 2 | Hard constraint, the reason the page exists |
| A second identity opening the same link is refused | 2 | Phase 2, requester binding |
| `sshd` accepts the certificate and the session opens | 3 | Phase 5 exit criterion |
| After `logout`, the same `ssh` is refused | 3 | Phase 5 exit criterion |

## The obstacles, and how each is handled

Every item here cost real time in the manual run.

**Ports.** Do not hardcode. The manual run collided with a VS Code debug build
squatting on 8080, and a hosted runner is no safer. The IdP gets its port from
`httptest`; `ssoosshd` and `sshd` get theirs by listening on `:0`, reading the
port, and closing. That last step has a theoretical reuse race, which is
acceptable here and much less likely to bite than a fixed port.

**Readiness.** Poll `/healthz` until it answers, with a deadline. Never sleep.
The server's startup does migrations, OIDC discovery, and pub/sub bootstrap,
and how long that takes is not knowable in advance.

**`http.public_url` must be set** to the origin the browser uses, or the OIDC
redirect URI and the CSRF origin check are built from the listen port and
silently do not match. This is the single most likely cause of a confusing
tier-2 failure.

**`authentication:` is a top-level config section**, not a subsection of
`http:`. The annotated sample said otherwise until it was fixed; the harness
should build config from a Go struct or a checked-in template, not from prose.

**Certificate lifetime.** The shipped default is 30 seconds, which would make
the reuse assertion flaky and the `ssh` assertion a race. The harness config
sets 8h, with one dedicated case using a deliberately short lifetime to cover
expiry.

**`sshd` needs an unlocked account.** A password-locked user is rejected with
`User <name> not allowed because account is locked` *before* any key is
considered — the failure looks like a certificate problem and is not. The job
creates a dedicated user (`useradd -m`, `usermod -p '*'`), maps the
certificate principal onto it with `AuthorizedPrincipalsFile`, and points
`TrustedUserCAKeys` at the test CA. Running `sshd` needs root, which a hosted
runner has via passwordless `sudo`.

**A private ssh-agent per test**, started by the harness with its own
`SSH_AUTH_SOCK`, never the ambient one. The logout assertion is meaningless
otherwise, and a leaked agent is a leaked process.

**Teardown is `t.Cleanup`, not deferred kills**, so a failing assertion still
stops `ssoosshd`, `sshd`, and the agent.

## Driving the browser

Two viable options, and the choice is not settled.

**chromedp** (recommended). Pure Go, so the whole harness is one language and
one command, and hosted Ubuntu runners already ship Chrome — no 115MB download
in the critical path. Weakness: no auto-waiting, so every interaction needs an
explicit `WaitVisible` against a stable selector.

**Playwright** (the proven one). This is what the manual run used, and its
auto-waiting made the SPA flow robust on the first attempt. Costs a browser
download (cacheable) and a second toolchain in the job, though Node and pnpm
are already set up there to build the UI.

**First implementation step is a spike**: port the three tier-2 assertions to
chromedp. If it fights the SPA's async loading, take Playwright — the harness
boundary (`harness/browser.go`) is drawn so that swap touches one file.

Either way, **add `data-testid` attributes** to the approval page's status,
option lists, and action buttons. Tier 2 currently has to match on prose, which
turns any copy edit into a test failure.

## The workflow

A separate `e2e.yaml` rather than extra steps on `build-test.yaml`: different
runtime, different failure modes, and its own artifacts. It should still gate
merges — it is the only job that checks the product rather than the parts.

```yaml
name: e2e
on:
  workflow_dispatch:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read

concurrency:
  group: e2e-${{ github.ref }}
  cancel-in-progress: true

jobs:
  e2e:
    name: end to end
    runs-on: ubuntu-latest
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: "go.mod"
          check-latest: true
          cache: true
      - uses: actions/setup-node@v4
        with:
          node-version: 22
      - uses: pnpm/action-setup@v4
        with:
          version: 10

      # The Go build cannot compile without it: server/frontend embeds dist/.
      - name: Build web UI
        run: make frontend

      - name: Build binaries
        run: go build -o bin/ ./cmd/...

      # Present on the hosted image, but asserting it beats a confusing
      # "no such file" three steps later.
      - name: Ensure sshd is available
        run: sudo apt-get install -y --no-install-recommends openssh-server

      - name: Run end-to-end tests
        run: go test -tags=e2e -count=1 -timeout=10m ./test/e2e/...

      - name: Upload diagnostics
        if: failure()
        uses: actions/upload-artifact@v4
        with:
          name: e2e-diagnostics
          path: |
            test/e2e/_artifacts/**
          retention-days: 7
```

Budget: roughly 3 minutes warm — UI build ~40s, Go build ~40s, tests ~60s.

**On failure it must explain itself.** Each tier writes to
`test/e2e/_artifacts/<test-name>/`: the `ssoosshd` log at DEBUG, the `sshd`
log at VERBOSE, the client's stdout and stderr, the issued certificate as
`ssh-keygen -L` output, and — for tier 2 — a screenshot at the point of failure
plus the browser console. Debugging a browser failure from an assertion message
alone is not realistic.

**No automatic retries.** A retried E2E test is a test nobody trusts. A flake
gets quarantined with an issue, not `retries: 3`.

## Also needed

- A `make test-e2e` target, so running it locally is one command with the same
  arguments CI uses.
- `test/e2e` added to `exclude-from-coverage.txt` — it is a harness, not
  covered code.
- A short `test/e2e/README.md`: how to run one tier, how to keep the browser
  visible, where artifacts land.

## Explicitly out of scope

- **Postgres.** The matrix doubles runtime to cover a layer the E2E is not
  aimed at. Worth a nightly run once `multi-instance-safety-plan.md` is acted
  on.
- **macOS and Windows runners.** The client is cross-platform and its agent
  handling genuinely differs (Pageant, the WSL relay), so this will matter —
  but not before the Linux path is trustworthy.
- **A real pocket-id.** Pulling a container for it would trade determinism and
  runtime for coverage of somebody else's code. The harness IdP exercises the
  same `go-oidc` path.
- **`pam_ssoossh`.** Blocked on the same amd64 container as phase 7; the
  harness IdP is written to be reusable when that unblocks.
- **Load or soak testing.** Different goal, different job.

## Open questions

1. Should tier 2 block merges from day one, or run advisory for a week first?
   Browser tests earn trust slowly, and a flaky required check trains people to
   re-run rather than read.
2. Should the harness IdP move to `internal/` once `pam_ssoossh` needs it
   (phase 7), or should it be duplicated? Sharing risks a test fixture becoming
   a de facto product dependency.
3. Does the tier-3 `sshd` belong in the PR gate at all, or only on `main`? It
   is the most environment-dependent tier and the least likely to break from an
   application change.
