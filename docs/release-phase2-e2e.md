# Phase 2: End-to-end harness, the merge gate

**Status: implemented.** Part of [release-plan.md](release-plan.md). `test/e2e/`
exists, all three tiers pass locally and in `.github/workflows/e2e.yaml`, and
`make test-e2e` is the one local command.

Implements [e2e-testing-plan.md](e2e-testing-plan.md), which holds the full
design. This phase is the delivery half: what gets built, in what order, and
what it must be able to do before phase 4 starts editing the signing path.

## Goal

Prove on every pull request that `ssoossh ssh login` produces a certificate a
real `sshd` accepts. Today nothing in CI checks any of it.

## Why this comes before the PAM work

Phase 4 edits `Approve`, `certTypeFor`, and the certificate options config.
The user path runs through all three. The unit suites are healthy and every
one of them mocks the far side of the wire, so the failure mode is specific
and known: the approval page fetches a path the server does not serve, the
CSP blocks the SPA's own JavaScript, the CSRF origin check rejects the
browser's POST, or `sshd` refuses a certificate that every Go assertion says
is correct.

Adding a second certificate type to code with no end-to-end coverage means
the first thing PAM can break is SSH login, silently.

## There is nothing to build on

`test/` does not exist. There is no `make test-e2e` target. This is a
greenfield phase, which is why it is budgeted as its own.

## Work

### 1. The harness skeleton

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

Behind `//go:build e2e`, so `go test ./...` stays fast and hermetic and the
cross-phase gate keeps its meaning.

Build `idp.go` and `server.go` first and get tier 1 green before writing
anything else. Everything downstream depends on being able to start a server
and authenticate against it; the browser and `sshd` tiers are additions to a
working base, not parallel work.

### 2. The identity provider

An `httptest.Server` implementing discovery, an authorization endpoint with a
real form, code exchange, and a JWKS, signing RS256 ID tokens with a
per-run key. Roughly 200 lines, standard library plus `golang.org/x/crypto`.

Deliberately a real provider rather than a stub inside `ssoosshd`: `go-oidc`
performs discovery, validates against the JWKS, and checks the nonce, so the
server's actual authentication path runs. Claims must include whatever
`authentication.fields.username` names, `preferred_username` by default.

`httptest.Server` also solves port allocation, which is not incidental. See
"Obstacles" below.

### 3. The three tiers

| Tier | What runs | Catches | Budget |
| --- | --- | --- | --- |
| 1, wire | Harness plus the `ssoossh` binary; approval driven over HTTP with a cookie jar walking the OIDC redirects | Request lifecycle, SSE delivery, narrowing, session and CSRF handling | ~10s |
| 2, browser | The same, with the approval page in a real browser | The SPA against the real server: routing, CSP, cookies, granted-versus-requested rendering | ~30s |
| 3, ssh | Tier 1 plus a real `sshd` trusting the test CA | The certificate actually authenticating a session | ~15s |

Tier 1 is worth having separately from tier 2 despite the overlap: when both
fail, the pair says whether the break is in the server or the UI, which is
the first question anyone asks.

### 4. The assertions

| Assertion | Tier |
| --- | --- |
| `ssh login` prints the approval URL while still waiting | 1 |
| Approving delivers a certificate over SSE | 1 |
| The certificate carries only server-permitted extensions, and no critical options | 1 |
| Denying resolves the waiting client with `denied`, no certificate | 1 |
| A second `login` reuses the valid certificate without a new approval | 1 |
| `logout` removes ssoossh's certificate and leaves an unrelated key untouched | 1 |
| An unauthenticated visitor is offered sign-in, not an approve button | 2 |
| The page shows trimmed options struck through before approval | 2 |
| A second identity opening the same link is refused | 2 |
| `sshd` accepts the certificate and the session opens | 3 |
| After `logout`, the same `ssh` is refused | 3 |

The third row is the hard constraint the approval page exists to enforce, and
the ninth is phase 2 of the delivery plan's requester binding. Both are
currently asserted only against fixtures.

### 5. The browser driver

**chromedp is the recommendation**, with a spike as the first step: port the
three tier-2 assertions and see whether it fights the SPA's async loading. It
is pure Go, so the harness stays one language and one command, and hosted
Ubuntu runners already ship Chrome.

Playwright is the fallback and the proven option: it is what the manual run
used, and its auto-waiting made the flow robust on the first attempt. It
costs a browser download and a second toolchain, though Node and pnpm are
already in the job to build the UI.

`harness/browser.go` is the boundary, drawn so the swap touches one file.

**Add `data-testid` attributes** to the approval page's status, option lists,
and action buttons as part of this work. Tier 2 otherwise matches on prose,
which turns any copy edit into a test failure.

### 6. The workflow

A separate `e2e.yaml`, not extra steps on `build-test.yaml`: different
runtime, different failure modes, its own artifacts. The full file is in
[e2e-testing-plan.md](e2e-testing-plan.md). It should gate merges. It is the
only job that checks the product rather than the parts.

Budget roughly 3 minutes warm: UI build ~40s, Go build ~40s, tests ~60s.

### 7. Diagnostics

On failure each tier writes to `test/e2e/_artifacts/<test-name>/`: the
`ssoosshd` log at DEBUG, the `sshd` log at VERBOSE, the client's stdout and
stderr, the issued certificate as `ssh-keygen -L` output, and for tier 2 a
screenshot at the point of failure plus the browser console.

Debugging a browser failure from an assertion message alone is not
realistic, and a test nobody can debug gets disabled rather than fixed.

**No automatic retries.** A flake gets quarantined with an issue, not
`retries: 3`.

### 8. Also needed

- A `make test-e2e` target, so running it locally is one command with the
  same arguments CI uses.
- `test/e2e` added to `exclude-from-coverage.txt`. It is a harness, not
  covered code.
- A short `test/e2e/README.md`: how to run one tier, how to keep the browser
  visible, where artifacts land.

## Obstacles, and how each is handled

Every item cost real time in the manual runs behind delivery phases 4 and 5.
They are documented in [e2e-testing-plan.md](e2e-testing-plan.md) and in the
project memory note on local end-to-end setup; the short form:

- **Ports.** Never hardcode. The manual run collided with a VS Code debug
  build on 8080. `httptest` allocates the IdP's port; `ssoosshd` and `sshd`
  listen on `:0`, read the port, and close.
- **Readiness.** Poll `/healthz` with a deadline. Never sleep. Startup does
  migrations, OIDC discovery, and pub/sub bootstrap.
- **`http.public_url` must be set** to the origin the browser uses, or the
  OIDC redirect URI and the CSRF origin check are built from the listen port
  and silently disagree. The most likely cause of a confusing tier-2 failure.
- **`authentication:` is a top-level config section**, not a subsection of
  `http:`. Build config from a Go struct or a checked-in template, never from
  the prose in a sample file.
- **Certificate lifetime.** The shipped default is 30 seconds, which makes
  the reuse assertion flaky and the `ssh` assertion a race. Harness config
  sets 8h, with one dedicated case using a short lifetime to cover expiry.
- **`sshd` needs an unlocked account.** A password-locked user is rejected
  with `User <name> not allowed because account is locked` before any key is
  considered, and the failure looks like a certificate problem. `useradd -m`,
  `usermod -p '*'`, `AuthorizedPrincipalsFile`, `TrustedUserCAKeys`.
- **A private ssh-agent per test** with its own `SSH_AUTH_SOCK`, never the
  ambient one. The logout assertion is meaningless otherwise.
- **Teardown is `t.Cleanup`**, not deferred kills, so a failing assertion
  still stops every process.

## Explicitly out of scope

- **Postgres.** Doubles runtime to cover a layer this is not aimed at. Worth
  a nightly run once multi-instance work happens.
- **macOS and Windows runners.** The client is cross-platform and its agent
  handling genuinely differs (Pageant, the WSL relay), so this will matter,
  but not before the Linux path is trustworthy.
- **A real pocket-id.** That is phase 7's compose stack, answering a
  different question.
- **`pam_ssoossh`.** Phase 5 extends the harness once the module exists. The
  IdP is written to be reusable for that.

## Exit criteria

- `make test-e2e` passes locally from a clean checkout.
- `e2e.yaml` gates pull requests and runs in under 5 minutes warm.
- A deliberately broken narrowing rule, or a deliberately wrong CSP, fails
  the suite. Verify the gate can actually fail before trusting it.

## Verification

- Run each tier in isolation.
- Break something on purpose in each tier and confirm the artifacts explain
  it: a wrong `public_url` for tier 2, a `TrustedUserCAKeys` pointed at the
  wrong CA for tier 3.
- Run the suite 20 times and count flakes. Anything above zero gets an issue
  before the gate becomes required.

## Open questions

Resolved with a default at implementation time, revisit if wrong:

1. **Tier 2 merge-gate timing.** Blocks from day one — `e2e.yaml` runs all
   three tiers in one job, matching the workflow both this file and
   [e2e-testing-plan.md](e2e-testing-plan.md) already specified. No advisory
   period was carved out.
2. **Tier-3 `sshd` in the PR gate.** Yes, same job as tiers 1–2.
3. **Harness IdP location.** Stayed in `test/e2e/harness`. Only matters once
   phase 5 (PAM) needs to reuse it.
