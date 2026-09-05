# Approval flow for GUI SSH clients

**Status:** proposal. Not scheduled.

**Anchors verified at:** `f948499`. `file:line` references drift; re-check
before relying on one.

A user connecting through a GUI SSH client (VS Code Remote-SSH, JetBrains,
Fork, Tower, Cyberduck, a DBeaver tunnel, Finder-mounted SSHFS) has no
terminal. `ssoossh ssh proxycommand` prints the approval URL to a stream
nobody reads, the connection stalls until the client's own timeout fires, and
the user sees a generic "connection failed" with no indication that a
certificate was ever wanted.

This proposes five changes that together make that flow work, plus one
security hardening on the approval URL that the flow makes newly relevant.

Primary platforms for this work are macOS and Windows.

## What this proposes

1. Decide where to write human-facing output by testing for a reachable
   terminal, not by detecting which application launched us.
2. When there is no terminal, open the browser and treat that as the message.
   Move the explanatory copy onto the approval page.
3. Stop blocking on a human inside a per-connection hook. Fail fast and let
   the user reconnect.
4. Move the hook from `ProxyCommand` to `Match exec`.
5. Coordinate concurrent invocations with a fail-open kernel advisory lock.
6. Bind an approval request to the first client that GETs it.

Items 1 through 5 need no new dependency.

## What exists today (verified)

`runLogin` prints the approval URL unconditionally before waiting
(`client/cmd/ssh_login.go:367`), then optionally launches a browser when
`try_open_browser` is set. That setting defaults to `false`
(`client/config/defaults.yaml:93`).

`openBrowser` (`client/cmd/ssh_login.go:518`) is best-effort with a 5 second
timeout: `open` on darwin, `rundll32 url.dll,FileProtocolHandler` on windows,
`xdg-open` elsewhere. It calls `Start`, not `Wait`, and reports failure to
`out` without failing the login. The `rundll32` form is the right choice over
`cmd /c start`, which mangles URLs containing `&`. No change proposed.

`ssh proxycommand` (`client/cmd/ssh_proxycommand.go`) calls `runLogin` with
stderr as the output writer, because stdout is the SSH stream from that point
on, then `syscall.Exec`s into the user-supplied relay command. It carries a
TODO about whether to locate `nc` itself.

The approval URL is `/approve/<request-id>`
(`server/controller/certrequests.go:102`). The handler documentation states
the security model plainly: "The request ID is the capability, it is an
unguessable UUID, and holding it is what authorizes waiting on the outcome"
(`server/controller/certrequests.go:174`). There is no binding to a client.

For PAM requests the certificate's principal is the requested local username,
not the identity that approves in the browser
(`internal/apitypes/certrequest.go:26`).

`ApprovalView.svelte` already renders `local_username` and `local_hostname`
(`frontend/src/lib/components/ApprovalView.svelte:166`) and already handles
approved, denied, and expired outcomes.

There is no cross-process locking anywhere in `client/` or `internal/`: no
`flock`, no `O_EXCL` sentinel, no `singleflight`.

`CreateUserRequest` (`internal/api/certrequest.go:35`) has no path that
returns an existing pending request. Every call creates a new one.

## 1. Detect the absence of a terminal, not the presence of an app

### The signal

Application detection is the wrong axis. It requires per-application
knowledge, it never covers the next client, and the obvious implementation is
wrong in a way that is easy to miss.

While testing this, a `ProxyCommand` running under a devcontainer observed
these variables with no Remote-SSH anywhere in the picture:

```
VSCODE_CWD, VSCODE_IPC_HOOK_CLI, VSCODE_ESM_ENTRYPOINT,
VSCODE_NLS_CONFIG, VSCODE_HANDLES_SIGPIPE
```

Matching on a `VSCODE_*` prefix therefore fires for a user running
`ssoossh ssh login` in VS Code's integrated terminal, where a terminal
plainly exists and a popup would be obnoxious.

The question that actually matters is whether a human can see anything we
write. That is directly testable and application-agnostic.

### The ladder

In order, first hit wins:

1. `isatty(stderr)` is true: write to stderr, exactly as today. `go-isatty` is
   already a direct dependency, used at `server/logging/utils.go:51`.
2. `/dev/tty` opens: write there. Covers `ssh` launched from a shell with
   stderr redirected or consumed.
3. Neither: no terminal exists. This is the GUI case. Open the browser.

Verified on OpenSSH 10.0p2. A `ProxyCommand` under `setsid` (no controlling
terminal, which is what a GUI launch looks like) reports `/dev/tty
UNAVAILABLE`; the same command under `script` reports `/dev/tty OPEN`. A
`Match exec` command reaches `/dev/tty` on the same terms.

Windows has no `/dev/tty`. Step 2 there is an attached-console test:
`GetConsoleWindow` returning NULL, or an empty `GetConsoleProcessList`.
`golang.org/x/sys` is already a direct dependency, so this costs no new
module.

### VS Code as a refinement only

A `ProxyCommand` inherits ssh's full environment. Injected
`SSH_ASKPASS`, `SSH_ASKPASS_REQUIRE`, `VSCODE_SSH_ASKPASS_NODE` and
`VSCODE_SSH_ASKPASS_MAIN` all arrived intact in a test ProxyCommand.

Remote-SSH sets `SSH_ASKPASS` to its own `askpass.sh` alongside
`VSCODE_SSH_ASKPASS_NODE` and `VSCODE_SSH_ASKPASS_MAIN`, so the askpass trio
is a reliable Remote-SSH marker (unlike the bare prefix). It could route a
prompt into VS Code's own UI.

This is optional polish, not the mechanism. It renders as an input box, which
displays a long URL badly and offers no way to click it. Keying on
`VSCODE_SSH_ASKPASS_MAIN` specifically, never on the `VSCODE_*` prefix, is the
only requirement if anyone builds it later.

## 2. The browser is the message

In the no-terminal case, launching the browser is the only signal that does
not depend on knowing which application spawned us, and the browser taking
focus is itself the notification. Unlike a transient dialog or toast it
persists until the user acts.

`try_open_browser` should default to `true` when the ladder reaches step 3 on
macOS or Windows. When a terminal exists the current default of `false`
stands: printing is sufficient and a browser launch would be presumptuous.

### The approval page carries the explanation

Because the browser is the channel, the approval page is the user interface,
and it may now appear with no context at all: the user clicked "connect" in
some application and a tab materialized. `ApprovalView.svelte` needs copy for
that case.

- Why the page appeared, naming the requesting user and host. The data is
  already rendered at `ApprovalView.svelte:166` and already sent by the client
  via `localIdentity()`.
- **That the user should return to their application and reconnect after
  approving.** This is the important one. Given item 3, the connection that
  triggered the request is already gone. Saying so converts a mysterious
  failure into an expected two-step.
- Outcome states, which already exist.

This is a copy change on a page we already ship, and it covers every GUI SSH
client without detecting any of them.

## 3. Fail fast instead of blocking on a human

VS Code's `remote.SSH.connectTimeout` defaults to 15 seconds (community
sources; not first-party documented, and worth confirming before relying on
the exact number). An interactive OIDC approval will not finish in that
window. Other GUI clients impose comparable limits.

So the connection that triggers a login cannot be the connection that benefits
from it, and no amount of improved prompting changes that. The hook must stop
pretending otherwise.

Proposed behavior in the hook:

1. Valid certificate present: proceed. No prompt, no delay. This is the common
   case and is unchanged.
2. No valid certificate: create the request, surface the URL by the ladder in
   item 1, then **fail this connection immediately** with a message naming
   what happened.

A one second clean failure plus a user-initiated retry beats a sixty second
hang that times out anyway and discards the work.

The complementary change is to stop reaching state 2 at all: refresh
proactively before expiry so the hook finds a live certificate. That is out of
scope here, and it is what eventually removes the need for items 3 through 5
entirely.

## 4. `Match exec` rather than `ProxyCommand`

`ProxyCommand`'s job is producing a byte stream. "Ensure a valid certificate"
is a precondition, not a transport. Overloading it is why the current
implementation has to `syscall.Exec` into `nc`, carries a TODO about locating
`nc`, and needs a long comment about an argv[0] shifting bug. Under
`Match exec`, ssh makes its own connection and all of that goes away.

### The output plumbing is strictly better

Verified with a `Match exec` command writing to both streams:

- **stdout is swallowed by ssh.** The marker never reached ssh's stdout.
- **stderr passes through** to ssh's stderr intact.
- **`/dev/tty` is reachable** when a controlling terminal exists.

Today a stray `Println` in the proxycommand path corrupts the session. Under
`Match exec` that is structurally impossible, while a usable human channel
remains.

### Two sharp edges

**`ssh -G` evaluates `Match exec`.** Verified: `ssh -F cfg -G testhost`
returned `user matched-user` and the probe command ran. Any tooling that
resolves configuration this way fires the hook. The command must therefore be
cheap and fully idempotent when a certificate is valid, and must never block.
Item 3 already requires this.

**It is evaluated once per ssh invocation.** One `ssh -G` plus one connect
produced two evaluations. A GUI client that spawns several ssh processes
evaluates it several times, with no single "first" process to designate. See
item 5.

### The trade being made

`Match exec` has worse failure semantics. Under `ProxyCommand`, returning an
error aborts the connection and our message is the stated reason. Under
`Match exec`, a non-zero exit means only that the block does not apply; ssh
continues and fails later at publickey authentication with a generic error.

Inverting the test (`Match ... exec "! ssoossh ssh ensure-cert"` with the block
setting something that fails loudly) recovers the abort but is genuinely
hacky. If adopted it needs a comment explaining why it is written backwards.

Net: `ProxyCommand` aborts well and plumbs output badly; `Match exec` plumbs
output well and aborts badly. Since item 3 makes clean abort less important
(we are failing on purpose and telling the user to retry) and item 1 makes
output plumbing more important, the trade favors `Match exec`.

## 5. Fail-open advisory lock

Several ssh invocations per user action, each creating its own request (item 6
rules out reuse), means several browser tabs and several pending approvals for
one connect. The user approves one; the rest fail.

### Kernel lock, not a sentinel file

The stale-lock failure mode belongs to sentinel files: create with `O_EXCL`,
delete on exit, and a crash or `SIGKILL` leaves a file nobody holds, requiring
liveness checks and staleness timeouts that are each their own bug.

Kernel advisory locks do not have that failure mode. `flock(2)` and
`LockFileEx` hold the lock on the open file descriptor, and the kernel
releases it when the process dies for any reason, including `SIGKILL` and
panic. The file may persist; the lock never does. Nothing to clean up, nothing
to detect.

`unix.Flock` and `windows.LockFileEx` come from `golang.org/x/sys`, already a
direct dependency.

### It must never be able to block a certificate

Design rules, so no lock failure can ever be what stops a login:

- **Fail open.** If acquisition fails for any reason (unusual filesystem,
  permissions, a network home directory where `flock` semantics are
  unreliable), log it and proceed unlocked. Worst case is today's behavior of
  N tabs. Annoying, never blocking.
- **Bounded wait.** If the lock is not acquired within a couple of seconds,
  give up and proceed. Never wait indefinitely.
- **Re-check after acquiring.** The winner may have already installed a
  certificate; the standard double-checked pattern turns the losers into
  no-ops rather than duplicate requests.
- **Local state directory, not `$HOME`.** Enterprise deployments frequently
  have network home directories, which is where `flock` gets flaky.

The lock is an optimization for the common case, never a correctness
requirement.

### This is a symptom

Nothing about issuing a certificate needs mutual exclusion. The lock exists
only because interactive login runs inside a per-connection hook that can
execute concurrently. Proactive refresh removes the concurrency rather than
managing it, and lets this be deleted.

## 6. Bind a request to the first client that GETs it

**Status: implemented** (2026-08-29). `middleware.ApprovalClaimMiddleware`
claims on the document GET; `service.CertRequestService.ClaimApprovalPage`
holds the claim/verify logic and mismatch logging; the SPA's
`/approval-unavailable` page carries the rejection and cookie-blocked copy.
This is the browser-level binding, layered on the identity-level binding
`bindRequester` already made on the first authenticated touch (which is what
"there is no binding" above predates). Note the Vite dev server serves SPA
documents itself, so the claim only runs against the embedded build.

### Why

The approval URL is a bearer capability by design and by documentation. Anyone
holding the request ID can open the approval page. For PAM requests the
issued principal is the requested username rather than the approver's
identity, so a request for a privileged account, approved by an
administrator who clicked through, yields a certificate for whoever created
the request.

Binding the request to the first client that fetches it kills replay: a URL
recovered later from shell history, a log file, a recorded screen share, or a
chat scrollback is already spent.

### Claim on the first GET, deliberately

On the first GET of `/approve/<id>`, set an `HttpOnly`, `Secure`,
`SameSite=Lax` cookie path-scoped to that request, and record its value on the
request row. Every later hit must present the matching cookie or be rejected.

`SameSite=Lax` specifically, so the top-level redirect back from the IdP still
presents the cookie. `Strict` breaks the return leg.

Claiming on GET makes GET state-changing, contrary to the usual expectation
that GET is safe. **This is intentional.** If the link travels through any
channel that fetches URLs (Slack or Teams unfurling, Outlook Safe Links,
Defender for Office, a corporate proxy), the scanner burns the request before
the victim ever clicks, and a phishing attempt fails closed. The legitimate
paths have no scanner in the middle: the client launches the browser directly,
or the user copies the URL out of their own terminal and pastes it into the
address bar.

Same-browser refreshes and back-button navigation are unaffected, since the
cookie is set. Only other clients are locked out. The handler needs a comment
recording that the unsafe GET is a deliberate control, or someone will
correct it later.

### The mismatch is a detection signal

In the legitimate flow, a claimed request being hit by a second agent
essentially never happens. That makes it a high-signal phishing indicator
obtainable for free from a control we are building anyway. Log it with the
claiming and rejected user agents plus timing, and consider surfacing it to
administrators.

### Cookie-blocked browsers need their own error

If a browser refuses cookies for the domain, every request looks like a new
client and the user hits the lockout on the OIDC return leg with no way out.
Detect the case where a request was claimed within the same navigation but no
cookie came back, and show a distinct message about cookies. Otherwise it is
an unbreakable loop that presents as a server bug.

### Rejection copy

Neutral and actionable. Scanner burns will be the most common cause by a wide
margin, so the page should say the link was already opened, that approval
links are single-use, and how to start a new login. It should not accuse
anyone of phishing on a page most often reached by a mail filter.

### Residual limit

Delivery channels that do not fetch links (SMS, a QR code, a screenshot of the
URL, a phishing page rendering it as plain text) bypass the tripwire, and
there the victim's browser is the legitimate first opener. Mismatch logging
does not catch that either, since there is no second agent.

What defends against that case is the approver reading `local_username` and
`local_hostname` before clicking. That display is doing the heavy lifting
against phishing and should be prominent rather than incidental.

## Deliberately rejected

### OS toast notifications

Investigated `zenity.Notify` on both primary platforms and rejected it as a
prompting channel.

On Windows it writes a temporary `.ps1` and spawns
`PowerShell -ExecutionPolicy Bypass -File`. That is a slow process spawn and
exactly the pattern endpoint protection flags; AppLocker or Constrained
Language Mode blocks it outright in some environments. The script calls
`CreateToastNotifier($title)`, passing the title as the AppUserModelID, and a
CLI binary has no registered AUMID, so this commonly throws. It then degrades
to a hidden-tray-icon balloon, then to `WTSSendMessage`, which is a modal
anyway.

On macOS it is `displayNotification` on `Application.currentApplication()`,
which from a CLI depends on notification permission having been granted to
something the user never knowingly installed. Denial is silent.

Structurally, a toast is fire-and-forget and this is a blocking, time-critical
action. It fails silently by default (Do Not Disturb, Focus Assist, full
screen, missing permission, unregistered AUMID), auto-dismisses, and carries
no click-through: the macOS path has no action, and the Windows template sets
`activationType="protocol"` without a `launch` attribute.

### A native dialog as a fallback for browser-launch failure

Considered and rejected: the two fail together. `open` and `osascript` both
require the process to be in the user's Aqua session;
`rundll32 url.dll,FileProtocolHandler` and `MessageBox` both require an
interactive window station. If the browser cannot launch, the dialog almost
certainly cannot display. On Windows it is worse, since `MessageBox` from
session 0 renders on a hidden desktop and hangs rather than failing.

### `github.com/ncruces/zenity` in general

Not rejected on quality. Verified at v0.10.15: MIT, no cgo, adds only itself
and `golang.org/x/image` to the module graph, and cross-compiles cleanly to
linux/amd64, windows/amd64 and darwin/arm64. Foregrounding is handled
correctly on both primary platforms (`MB_SETFOREGROUND` on Windows;
`NSApplicationActivationPolicyRegular` plus `app.activate()` on macOS). On
Linux it shells out to `qarma`, `zenity` or `matedialog` and exposes
`IsAvailable()` for gating; macOS needs only `osascript`; Windows needs
nothing.

It is simply not needed for the flow above, because the browser covers the
happy path and the failure cases are correlated with browser launch. The one
surface where it would be uncorrelated is an error occurring **before** an
approval URL exists (server unreachable, network down, broken config), where
there is no page to open and the failure currently vanishes. That is a small,
well-defined use worth revisiting on its own merits, not as part of this.

Alternatives surveyed and rejected: `gen2brain/dlgs` (unmaintained since 2022,
and the basis zenity's Windows port improved on), `sqweek/dialog` (cgo on both
Linux and Darwin, which breaks musl builds and cross-compilation),
`gen2brain/beeep` (notifications only, 10+ modules), `esiqveland/notify`
(Linux D-Bus only), and full toolkits like fyne or webview (cgo, absurdly
oversized for a message box). `pkg/browser` and `cli/browser` would replace
`openBrowser`, which already works and adds no dependency.

### Server-side request deduplication

Returning an existing pending request for a repeated call would solve the
concurrency problem in shared state rather than with a local lock. Rejected:
requests are single-use by design, and item 6 makes reuse incoherent, since
a second caller would have nothing well-defined to bind to. Concurrency is
therefore a client-side problem, which is what makes item 5 necessary and
strengthens the case for proactive refresh.

### `/dev/tty` as the primary channel

Rejected as primary because the GUI case (`setsid`, no controlling terminal)
is exactly where it is unavailable, which is verified above. It is valuable
as step 2 of the ladder, not as the answer.

## Sequencing

1. Terminal-detection ladder, replacing the current unconditional print. Low
   risk, no behavior change when a terminal exists.
2. Fail-fast in the hook. This is the change that makes GUI connections
   comprehensible, and it is a prerequisite for everything else.
3. Approval page copy, including the reconnect instruction.
4. Default `try_open_browser` to true in the no-terminal case on macOS and
   Windows.
5. First-GET binding, with mismatch logging and the cookie-blocked error path.
   Independent of 1 through 4; can land in either order.
6. Fail-open advisory lock.
7. `Match exec` migration, with the `ProxyCommand` path kept working through a
   deprecation period since it is in users' ssh_config files.

Proactive refresh is the follow-on that retires 5, 6, and most of 2.

## Testing

Per `.claude/rules/test-go.md`, table-driven throughout, with the terminal
ladder and the browser launcher behind interfaces so tests stay hermetic.
`openBrowser` is currently not behind one, which is tolerable for
`exec.Cmd.Start` but not for anything that can block.

- Ladder selection: stderr-is-tty, tty-unavailable-but-`/dev/tty`-open,
  neither. The third case is reproducible under `setsid`.
- Fail-fast: valid certificate proceeds; missing certificate returns promptly
  with a message rather than waiting.
- Lock: contended acquisition, acquisition failure proceeds unlocked, bounded
  wait expires and proceeds, winner's certificate is observed by losers on
  re-check.
- Binding: first GET claims and sets the cookie; second client rejected; same
  client with cookie succeeds across the IdP redirect; cookie-blocked browser
  gets the distinct error; mismatch is logged.
- `Match exec`: exit codes, idempotence under repeated evaluation, and that a
  valid certificate makes it a fast no-op.

Cross-platform items (Windows console detection, `LockFileEx`, `rundll32`
launch) need coverage in the existing cross-platform suite; see
`docs/dev/cross-platform-testing.md`.

## Provenance: what was verified and how

Verified by direct test in a Linux devcontainer against OpenSSH 10.0p2:

- A `ProxyCommand` inherits ssh's environment, including injected
  `SSH_ASKPASS` and `VSCODE_SSH_ASKPASS_*`.
- Ambient `VSCODE_*` variables leak into that environment absent Remote-SSH.
- `/dev/tty` is unavailable under `setsid`, available under `script`, for both
  `ProxyCommand` and `Match exec`.
- `ssh -G` evaluates `Match exec` and applies the resulting block.
- `Match exec` stdout is discarded by ssh; its stderr reaches ssh's stderr.
- One `ssh -G` plus one connect evaluates `Match exec` twice.

Verified by reading the module at `v0.10.15` in the module cache: zenity's
license, dependency graph, per-platform backends, foregrounding flags, and
notification implementations. Cross-compilation verified by building.

Verified by reading this repository at `f948499`: the approval URL shape and
capability model, PAM principal semantics, the absence of locking, the absence
of request deduplication, `try_open_browser`'s default, and the fields
`ApprovalView.svelte` renders.

From community sources rather than first-party documentation, and worth
re-checking: `remote.SSH.connectTimeout` defaulting to 15 seconds, and the
specific names VS Code Remote-SSH uses for its askpass variables.

Not verified, because the test environment is Linux: macOS and Windows
behavior throughout. The POSIX mechanisms (environment inheritance,
controlling-terminal semantics) carry to macOS; Windows needs its own
verification for console detection and `LockFileEx`.

## Open questions

- Should the hook create a request at all when it is going to fail the
  connection anyway, or only report that no valid certificate exists and let
  the user run `ssoossh ssh login` explicitly? Creating one makes the retry
  immediate but produces a request nobody may ever approve.
- Binding to the originating client's SSE connection rather than to a browser
  cookie is a stronger control, and it conflicts directly with fail-fast:
  the client is gone seconds later, so there is nothing to anchor to. Worth
  deciding deliberately rather than during implementation.
- How long should a claimed-but-unapproved request stay claimed? A user whose
  browser died mid-flow currently has to restart, which is cheap, but the
  interaction with existing request expiry needs checking.
- Does `try_open_browser` remain a single boolean once its default becomes
  conditional on terminal presence, or does it need a third state
  (`auto`/`always`/`never`)? A conditional default that a boolean cannot
  express is a common source of confusion, and the setting is enforced through
  Windows policy (`client/config/policy_windows.go`), so changing its shape
  has a documented downstream cost in
  `https://mnestor.github.io/ssoossh/hosts/client-enforcement/`.
