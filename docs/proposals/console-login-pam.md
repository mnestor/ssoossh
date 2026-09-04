# Console login through ssoossh

**Status: the server half is built.** The `console` certificate type,
`POST /api/certs/console`, the code-resolution endpoint, the per-type
approval budget, the `allowed_networks` gate, and the `/console` and
`/c/<code>` pages all exist. The module that drives this from a console is
being written separately in C, so the `mode=console` argument, the QR
rendering and `prompt=enter` described below are that module's design and
not this repo's; `pam_ssoossh` keeps doing `sudo` only.

Three decisions below moved during implementation, and the sections that
state them are the ones to read rather than this summary:

- The typed-code channel is what shipped. QR (channel 3) and push
  (channel 4) are still deferred; the server returns the short `/c/<code>`
  URL a QR would encode.
- Code submission is rate limited per session *and* per source address
  (`http.console_code_rate_limit`), not per source address alone.
- Resolving a code emits `cert.code_resolved`, which the design did not
  call for. It is the moment an unauthenticated machine's login acquires a
  named human, which is the step the consent-phishing case turns on.

Every `file:line` anchor below was verified against `a009511` (2026-09-04)
and has drifted since.

> **Before planning from this document**, re-run the checks in
> [Provenance](#provenance-what-was-verified-and-how), and read
> [Decisions](#decisions-and-the-reasoning-behind-each) rather than only the
> decisions. Items resting on judgement about operator behaviour, not on
> facts about the code, are marked **[judgement]** and are the ones worth
> re-opening first.

This is the design for the item already parked in two places:

- [design-brief.md](../internals/design-brief.md) "Future": *"Console login
  PAM module: displays a code the user types into the web UI (machine needs
  server reachability, not a browser). Same four checks. Per-host server
  setting to disallow or group-restrict console logins, failing before a
  certificate is minted."*
- [features.md](../guide/features.md) roadmap: *"Console-login PAM and
  QR-code approval, for a machine with no browser in front of it."*

**Depends on** [pam-principal-source.md](pam-principal-source.md), which
should land first. That change makes a PAM certificate carry the approver's
accounts instead of the local account name the module sent, and this design
assumes it: several passages below say "today" and "after the fix" for that
reason.

Related documents this one does not edit:

- [gui-client-approval-flow.md](gui-client-approval-flow.md) solves the
  adjacent problem for GUI SSH clients (no terminal to print a URL to). It
  proposes the same request-to-client binding this design depends on, and
  its "the browser is the message" reasoning is the opposite of the one
  here: a console has a human in front of it but no browser, a GUI client
  has a browser but no human-readable stream.
- [source-address-restrictions.md](source-address-restrictions.md) owns the
  source-network machinery the per-host gate below reuses.

## What this proposes

1. **One `.so`, two modes.** Extend `pam_ssoossh` with a `mode=console`
   module argument rather than shipping a second PAM module. Everything
   except the approval channel and the certificate type is already shared.
2. **A new `console` certificate type**, so an operator can gate and time a
   whole interactive session differently from a single `sudo`.
3. **A typed short code as the baseline channel.** The console displays an
   8-symbol code and a short verification URL; the approver signs in to the
   web UI and types the code. The code is a *lookup key for an
   authenticated approver*, never a capability that yields a certificate.
4. **A QR code in the terminal as the preferred channel** where the console
   can render it, removing the typing entirely. Code entry stays as the
   fallback and is what display managers get.
5. **Approval context on the page**: hostname, PAM service, tty, local
   account, source IP. Without these the approver cannot tell a legitimate
   console login from one an attacker started.
6. **The typed code is the consent-phishing control**, not just a UX
   device. `bindRequester`'s own comment already names a code the client
   displays and the browser has to match as the missing defence; the console
   flow supplies it by construction, for the console type and for `sudo`.
7. **A per-type `client_timeout`**, bounded by today's global as a ceiling.
   The approval window is the attacker's working time in the phone-call
   case above, and five minutes is a lot of it. Console defaults shorter.
8. **Per-host gating stays in the PAM stack**, where it already works.
   `cert_options.<type>.require` is one condition for a whole deployment and
   does not survive a fleet, but the answer is not to send a group to the
   server: `pam_succeed_if` above the ssoossh line is host-side,
   unforgeable, and fails before any network call.
9. **A source-network gate**, not a hostname gate, for the server-side half
   of per-host policy. A hostname is self-reported by an unauthenticated
   caller; the source address is observed by the server.

Not proposed: offline (network-down) console login, user provisioning, and
session re-authentication. See
[Deliberately rejected or deferred](#deliberately-rejected-or-deferred).

## What exists today (verified)

The `sudo` flow, end to end:

| Step | Where |
| --- | --- |
| `pam_sm_authenticate` is the only management group implemented | `pam_ssoossh/pam.go:26`, with `pam_sm_setcred` returning `PAM_SUCCESS` at `:30` |
| Module arguments parsed into `config` | `pam_ssoossh/args.go` (`parseArgs`); defaults `skew-tolerance=2s` (`:15`), `timeout=60s` (`:20`) |
| Ephemeral keypair, request, wait, validate | `pam_ssoossh/auth.go:25` (`Authenticate`) |
| Approval URL shown to the human | `pam_ssoossh/auth.go:75`, through `Conversation.Info` |
| The only channel to the human is `PAM_TEXT_INFO` | `pam_ssoossh/conversation.go` (`send_text_info`); no `PAM_PROMPT_*`, so the module never reads a response |
| The four certificate checks | `pam_ssoossh/checks.go:73`, `:93`, `:117`, `:158` |
| Local-account authorization | `checkPrincipal` at `pam_ssoossh/checks.go:117`, exact match or `principals-map` |
| Request creation endpoint | `server/controller/certrequests.go:64` (`POST /api/certs/pam`), rate limit default 10/s at `server/config/types_http.go:251` |
| Approval page path | `server/controller/certrequests.go:102` (`approvalURL`), `/approve/<uuid>` |
| The request ID is the credential for the SSE wait | `server/controller/certrequests.go:53-63` comment, and `eventsHandler` is unauthenticated |
| Per-type policy table | `server/service/certtypepolicy.go:140` for `CertificateTypePAM` |
| Approver authorization | `server/service/certrequest.go:678` (`checkApproverAuthorization`) |
| Browser-level binding of the approval page | `server/service/certrequest_claim.go` (`ClaimApprovalPage`) |
| Identity-level binding | `bindRequester`, called from `Approve` at `server/service/certrequest.go:645` |

Facts that shape the design:

- **A PAM certificate carries the local account, not the approver, and
  that is being fixed.** `policy.principals(req.Username, identity, selected)`
  is called at `server/service/certrequest.go:1192`, and the PAM type's
  implementation ignores both `identity` and `selected`, returning
  `[]string{pamUsername}` (`server/service/certtypepolicy.go:145-147`) -
  the account name an unauthenticated caller sent. The fix is
  [pam-principal-source.md](pam-principal-source.md): principals become
  `identity.Username` plus `identity.OtherAccounts`, and the host's
  `principals-map` decides which local account that authorizes. **Everything
  below assumes that has landed.** It changes the console threat model
  materially: an attacker who names `root` at the console gets a
  certificate naming the approver's own accounts, which check 3 refuses
  unless the host's root-owned map says otherwise.
- **The consent-phishing gap is already recorded, with its answer.**
  `bindRequester` stops one user approving another's *pending* request, but
  its doc comment (`server/service/certrequest.go:721-725`) says plainly
  that it *"does not defend against a user being tricked into approving a
  request an attacker created for them: that consent-phishing case needs a
  verification code the client displays and the browser has to match, which
  is deliberately out of scope here."* The console flow is that code. (The
  `docs/security-review-2026-08-11.md` that comment cites is not in the
  repo; the comment is the only surviving reference.)
- **The request carries no host context.** `model.CertificateRequest`
  has `Username` (`server/model/certificate_request.go:34`) for PAM
  requests, and `LocalUsername`/`LocalHostname` (`:51-52`) which the model
  comment says are *"set only for `CertificateTypeUser` requests"*. A PAM
  request therefore reaches the approval page with a username, requested
  options, and the server-observed source IP, and nothing else.
- **The certificate type is a `CHECK` constraint in three places.** The
  model tag at `server/model/certificate_request.go:29`, and
  `chk_certificate_requests_type` in both
  `server/resources/migrations/sqlite/20260101000000_init.up.sql:56` and
  the postgres file at the same line. `server/model/enums.go` states this
  explicitly: adding a type needs all three together.
- **Timing is one global budget, split by a fixed fraction.**
  `cert_options.client_timeout: 5m` (`server/config/defaults.yaml:981`) is
  the whole thing; `SigningGrace() = ClientTimeout/10` and
  `ApprovalTTL() = ClientTimeout - 2*SigningGrace()`
  (`server/config/types_certificates.go:39-61`), so the human's share of the
  default is 4m. `ApprovalTTL`'s own comment calls it *"shared across the
  certificate types ... not a per-type concept"*, which this design changes,
  and it feeds the stranded-request sweep, the resolved-outcome cache and
  the sweep interval. Separately, `cert_options.pam.valid_duration: 30s`
  (`:912`) is the certificate's own life, short because it is validated once
  and discarded.
- **The e2e tier for PAM is real.** `TestPAMStack*` in
  `test/e2e/pam_stack_test.go` compiles `pam_ssoossh/testing/pamtest.c`,
  installs a dedicated `/etc/pam.d` service, and drives a real
  `pam_authenticate` (`docs/dev/pam-e2e-testing.md`). A console mode can be
  tested the same way rather than by hand.

## The problem, stated precisely

`sudo` runs inside a session the user already established, on a machine
where they usually have a browser or at least a copyable terminal. Printing
`https://sso.example.com/approve/6f1c9d2e-...` and asking them to open it
works because they can select the text.

A console login has none of that:

- **No copy/paste.** A physical tty, a serial console, an IPMI/iDRAC/BMC
  HTML5 viewer, a KVM, or a VM console in Proxmox/vSphere. Text on that
  screen exists only to be read by eye.
- **No browser on the machine**, by definition, and often no window manager
  at all.
- **The user has not logged in yet**, so nothing about them is known to the
  host beyond the username they typed at the `login:` prompt.
- **The stakes are higher.** A `sudo` certificate authorizes one command
  invocation. A console certificate authorizes an interactive session with
  the machine's full local authorization surface.
- **Console is the break-glass path.** It is the thing people reach for
  when the network is broken, which is exactly when this flow cannot work.

A 36-character UUID inside a 50-character URL is roughly 86 characters to
transcribe by eye. That is the thing to fix.

## Channel candidates

Four ways to move an approval from the console to a browser. They are not
exclusive; the recommendation is 1 plus 2 plus 3, with 4 deferred.

### 1. Typed short code (baseline, always available)

The console shows:

```
ssoossh console login for account: alice
Host: web01   tty: tty1

    Go to:  https://sso.example.com/console
    Code:   K7M4-QP2X

Waiting for approval (expires in 1m36s). Ctrl-C to cancel.
```

The approver opens the verification URL on any device, signs in via OIDC if
not already, types the code, sees the request context, approves.

**Code format.** 8 symbols from Crockford Base32 (`0123456789ABCDEFGHJKMNPQRSTVWXYZ`,
which omits `I`, `L`, `O`, `U`), rendered as two groups of four. That is 40
bits. Input is normalized before lookup: uppercased, non-alphanumerics
stripped, then Crockford's decoding aliases applied (`I`/`i`/`l`/`L` become
`1`, `O`/`o` becomes `0`). `U` is excluded from the alphabet so that no
accidental profanity is generated and so `V` is unambiguous.

Why 40 bits rather than the ~20 bits RFC 8628 accepts for device codes: a
device code is guessed by an attacker trying to hijack *their own* pending
authorization, and RFC 8628 leans on rate limiting. Here the guessing risk
is different and worse, see
[The code is not a capability](#the-code-is-not-a-capability). 40 bits keeps
the margin regardless of how the rate limiter is configured, and 8 symbols
is still comfortably typeable.

**Lifetime.** The code is minted with the request and dies with it, bounded
by `cert_options.client_timeout`. There is no separate knob, for the same
reason `decisions.md` gives for rejecting separate approval and signing
windows.

### 2. Complete verification URL (one step instead of two)

Also print `https://sso.example.com/c/K7M4QP2X`, the equivalent of RFC
8628's `verification_uri_complete`. A user with a phone types 32 characters
once instead of navigating to a page and then typing a code into a box.
Resolving that path still requires an authenticated session; it is a
shortcut through the UI, not around the auth.

### 3. QR code in the terminal (preferred where it renders)

Encode the complete verification URL as a QR code drawn with Unicode
half-block characters (`U+2580`, `U+2584`, `U+2588`, space), two QR rows per
terminal row. The user points a phone camera at the console and lands on the
approval page. No transcription at all.

**It fits.** A ~40-character URL encodes in QR version 3 at error
correction level L (53 bytes of byte-mode capacity, 29x29 modules). With a
2-module quiet zone that is 33 columns and 17 terminal rows; with the
standard 4-module quiet zone, 37 columns and 19 rows. Both fit an 80x24
console, though 19 rows leaves little room for the surrounding text, so a
2-module quiet zone is the default and the URL is kept short (`/c/<code>`,
not `/approve/<uuid>`, which would push it to version 4 or 5).

**Where it does not render**: display managers (GDM, SDDM, LightDM) show
`PAM_TEXT_INFO` in a label sized for a sentence, and serial consoles or
BMC viewers may not have a font with block characters. So QR is
`qr=auto` by default: emit it only when the module can see a plausible text
console (a `PAM_TTY` that looks like a tty, a UTF-8 locale, and a terminal
at least 40 columns wide), and always print the code and short URL
alongside it. `qr=off` and `qr=on` force the decision.

**Where the QR is rendered** is a real choice. Rendering in the module needs
a QR encoder linked into a security-critical `.so`. Rendering on the server
and shipping the ANSI art in the create-response keeps the module
dependency-free but means writing server-supplied bytes to a terminal, which
is an escape-sequence injection primitive if the server is ever compromised
or spoofed. Recommendation: **render in the module** with a small pinned
encoder, and regardless of which side renders, whitelist the characters
before they reach the tty.

### 4. Push to the user's registered device (deferred)

The server already has a notification subsystem (`server/notify`,
`server/service/notification.go`). It could look up the user whose
principals include the requested local account and push "console login
requested on web01, approve?".

Deferred, because request creation is unauthenticated. Anyone who can reach
`POST /api/certs/console` could make a stranger's phone buzz, which is both
a nuisance channel and a phishing setup ("approve the login I just sent
you"). If it is built, it must be opt-in per deployment, rate limited per
target user rather than per source, and the notification must carry the full
context (host, tty, source IP) rather than a bare approve link.

## Security: what changes when the certificate buys a session

### Consent phishing, and why the code is the control

Someone standing at an unattended console types `alice` at the `login:`
prompt. The module shows a code. The attacker phones Alice: "IT here, we
are re-enrolling your console access, can you approve the code on your
screen, it is K7M4-QP2X."

`bindRequester` does not stop this and says so. Nobody has touched the
request yet, so Alice's first authenticated touch *is* the binding: she
claims it, then approves it. The recorded answer to exactly this case is
"a verification code the client displays and the browser has to match"
(`server/service/certrequest.go:721-725`), which is what the console flow
already is. The code raises the bar from "click this link" to "read me the
eight characters on that screen", which is a phone call an attacker has to
make and a lie a victim can be trained to refuse.

What it buys is also much smaller after
[pam-principal-source.md](pam-principal-source.md). The certificate Alice's
approval mints names Alice's accounts, so the attacker gets a session as
Alice on a host where the map already allows her, not a session as whatever
account they typed at the prompt. Escalation is off the table; impersonation
of the approver is what is left.

It does not eliminate that. A human who will read a code aloud is a human
who will read a code aloud. Three mitigations narrow what it buys:

**A shorter approval window.** The attack is a live phone call: dial,
explain, get the victim to a browser, through an OIDC login, into the code
box, and clicking approve, all before the request expires. Today that clock
is `cert_options.client_timeout`, one global 5m budget. Cutting the console
type to 2m roughly halves the working time, costs no new mechanism, and a
code that dies mid-call is a failed attack that also leaves an
expired-request row somebody can notice. See
[Config shape](#config-shape) for the knob and the ceiling rule.

There is a floor, and it is not the technical minimum. Too short and
legitimate first approvals fail, people retry, and a flow people habitually
retry is a flow people learn to click through without reading. That is a
worse outcome than a longer window.

**Context on the approval page.** The approver must see what they are
approving:

| Field | Source | Trust |
| --- | --- | --- |
| Local account | already sent (`Username`) | self-reported |
| Hostname | new wire field | self-reported |
| PAM service (`login`, `gdm`, `sddm`) | new wire field, from `PAM_SERVICE` | self-reported |
| tty / display | new wire field, from `PAM_TTY` | self-reported |
| Remote host | new wire field, from `PAM_RHOST` (empty for a real console; non-empty means it is not one) | self-reported |
| Source IP | already recorded (`SourceIP`) | server-observed |
| Requested at | already recorded (`CreatedAt`) | server-observed |

Everything the module sends is self-reported by an unauthenticated caller
and the UI must present it as such, the same way `source-address` handling
already treats client-supplied addresses as unverified input. Its value is
that it lets a human notice "I am at my desk, why is there a console login
on rack07". A non-empty `PAM_RHOST` on a request claiming to be a console
login is worth flagging in the UI outright.

**Refuse `root` unless asked for.** With
[pam-principal-source.md](pam-principal-source.md) landed this is no longer
the escalation control it would have been, a certificate naming the
approver's accounts cannot authorize `root` unless the host's
`principals-map` says that identity may become root. It stays anyway for two
reasons that survive: root console login is the recovery path that must keep
working when ssoosshd is down, so routing it through ssoosshd is usually a
mistake, and refusing before a keypair is generated saves a pointless round
trip. This is a host-side check, cheap, and it fails without a network
round trip.

### The code is not a capability

The request ID is the credential for the SSE wait
(`server/controller/certrequests.go:53-63`); anyone holding it can connect
to `/api/certs/requests/<id>/events` and receive the certificate. That is
already true today, and the browser already learns the ID because it is in
the `/approve/<id>` URL, so this design does not change the property. What
it must not do is create a *new*, shorter, unauthenticated path to that ID.

Rules, all of which are load-bearing:

1. **Code resolution requires an authenticated session.** `GET /c/<code>`
   and the code-submission endpoint both sit behind `sessionAuthMiddleware`.
   An unauthenticated caller gets the login redirect, never a request ID and
   never a 404-vs-200 oracle that distinguishes a live code from a dead one.
2. **The code never appears in an SSE payload, an unauthenticated response,
   or an audit `Detail` map.** The existing enrollment code is already
   treated this way (`server/service/certrequest.go:863`: *"The code itself
   is deliberately absent: it is a bearer credential"*).
3. **Rate limit code submission per session and per source address**, and
   count failures against the session rather than only the IP, so a single
   compromised account cannot grind through the space from many addresses.
   At 40 bits with a handful of live codes, even a generous limiter leaves
   an infeasible search; the limiter is there to make the margin
   independent of how many requests are pending.
4. **Codes are unique among live requests**, enforced by a unique index on
   the column with the terminal rows excluded (or by retrying on conflict at
   mint time). A collision would let one approver's typed code resolve to a
   stranger's request.
5. **One code, one request, one shot.** A submitted code that resolves is
   immediately bound to the submitting session by the existing claim
   machinery; a second session submitting the same code is refused the same
   way a second browser opening `/approve/<id>` is refused today
   (`ClaimApprovalPage`, `server/service/certrequest_claim.go`).

### The browser-level claim, revisited

`ClaimApprovalPage` binds `/approve/<id>` to the first browser that GETs it,
which is what lets a link scanner burn a phishing link
(`server/service/certrequest_claim.go:84-88`). A code-entry flow has no
link to scan, so that specific protection is not needed, but the mechanism
still applies once the code resolves and the browser is redirected to
`/approve/<id>`. Order matters: **claim at code submission**, not at the
redirect target, so that a race between two sessions submitting the same
code is resolved before either sees the request detail.

### Per-host gating is the PAM stack's job, not a wire field

`cert_options.<type>.require` is one condition for the whole deployment.
That is workable for a homelab and breaks in a fleet: `web01` is
administered by the web team, `db07` by the DBAs, `rack07-bmc` by two people
in facilities. A single server-wide group would have to be the union of all
of them, at which point it gates nothing.

The instinct is to let the PAM stack name the group and send it to the
server to enforce. **Do not.** PAM already gates on groups, host-side, and
every property of the native mechanism is better:

```
# /etc/pam.d/login
auth  [success=ignore default=die]  pam_succeed_if.so  user ingroup console-web01 quiet
auth  sufficient                    pam_ssoossh.so     mode=console \
        server=https://ssoosshd.example.com \
        trusted-ca-file=/etc/ssoossh/ca.pub
auth  include                       common-auth
```

| | `pam_succeed_if` above the module | a group sent to the server |
| --- | --- | --- |
| Where it runs | on the host, in the stack | on the server, at approval |
| Who can change it | root on that host | anyone who can reach the API, since the request is unauthenticated |
| When it fails | before any keypair, request row, or network call | after a human has been asked to approve |
| Who checks it | a module maintained by linux-pam | code this project would have to write and test |
| Fleet configuration | already whatever manages `/etc/pam.d` | a second place to keep in sync with the first |

The forgeability line is the decisive one. A field the module sends is
untrusted input: it could only ever *narrow* the effective gate
(`require` AND the requested group), so an attacker cannot use it to widen
anything, but they can **omit** it and fall back to the server-wide
condition alone. That makes it a control that silently stops applying
exactly when someone is attacking it, which is worse than no control,
because the operator believes it is there. `pam_succeed_if` cannot be
omitted by anyone who is not already root on the host.

`pam_access` with `/etc/security/access.conf` is the same argument if the
gate wants to be per-tty or per-origin rather than per-group, and it
composes with the ssoossh line the same way.

#### What is still ours to enforce

Two host-side gates the module already owns, and one gap:

- **`principals-map`** (`pam_ssoossh/checks.go:117`) is the ssoossh-native
  per-host account gate: it decides which certificate principals may assume
  which local account, it is root-owned, and the module re-checks it after
  the certificate comes back. It works on a host with no directory at all.
- **The host gate runs first, so it constrains the whole flow.** Any
  account that reaches `pam_ssoossh.so` is one the host has already decided
  may console in, and the approval that follows cannot widen that: after
  [pam-principal-source.md](pam-principal-source.md) the certificate names
  the approver, and `checkPrincipal` re-checks that against the host's own
  map before the module returns success. Both ends of the decision are
  root-owned and local.
- **The gap** is a host with no directory-backed NSS groups, where
  `pam_succeed_if user ingroup` has nothing to resolve and `principals-map`
  has to enumerate accounts one at a time. That is a `principals-map`
  enhancement (a group key resolved against local groups), not a reason to
  trust a field on the wire. See [Open questions](#open-questions).

What no host-side gate constrains is *who approved*. Alice can approve a
console login for `bob` on a host where both accounts are permitted, and
nothing refuses it. That is a deliberate act by a named identity, it lands
in the decision audit record with her subject, username, groups and source,
and treating it as an attack would also forbid the legitimate version of it
(an operator unlocking a console for a colleague). The audit trail is the
control here, not a policy gate.

### Per-host policy, on a signal the server can verify

The design brief asks for a *"per-host server setting to disallow or
group-restrict console logins, failing before a certificate is minted."*
The **group-restrict** half of that is the PAM stack's, per the section
above, and it fails earlier than the brief asked for: before the request is
created at all. What is left for the server is **disallow**, and it cannot
rest on the hostname, which is a string an unauthenticated requester typed
(the exact reasoning that got host certificates declined,
`docs/project/decisions.md`). Do it on the source address instead, which the
server observes, reusing the network machinery from
[source-address-restrictions.md](source-address-restrictions.md):

```yaml
cert_options:
  console:
    # Requests from outside these networks are refused at creation,
    # before a keypair is certified. Empty means no network gate.
    allowed_networks:
      - 10.20.0.0/16      # the datacenter management VLAN
      - 192.168.50.0/24   # the lab
```

"Fails before a certificate is minted" is satisfied by refusing at
`CreateRequest` rather than at approval, so a request from a disallowed
network never reaches a human at all.

## One module or two

**Recommendation: one `.so`, a `mode` argument.** The user's framing allowed
for a second module; here is why one is better.

What is identical between `sudo` and console: the ephemeral keypair, the
four checks (`pam_ssoossh/checks.go`), `principals-map` handling, the
trusted-CA file, skew tolerance, the logger and its syslog wiring, the
interrupt and timeout handling, the return-code mapping in
`classifyRequestError`, and the entire build, packaging, glibc floor check,
lint pass, and e2e harness.

What differs: which endpoint is called, what is written to the conversation,
one default (`timeout`), and two host-side guards (`allow-root`, `qr`).

A second `.so` duplicates two goreleaser build ids, two nfpm packages, the
`make pam` / `test-pam` / `lint-pam` targets, the cross-compile matrix, and
the PAM e2e tier, in exchange for roughly a hundred lines of difference. It
also creates a real hazard: two modules whose four security checks must stay
in lockstep, where a fix applied to one and not the other is a silent
vulnerability.

The mode selection stays explicit and fails closed:

```
mode=sudo     (default when absent, exactly today's behaviour)
mode=console  (code / QR channel, console certificate type)
```

An unrecognized `mode` is a configuration error that fails the
authentication with `PAM_NO_MODULE_DATA`, matching how a missing
`trusted-ca-file` is handled (`pam_ssoossh/auth.go:31-33`), rather than
silently falling back to `sudo`.

## Certificate type: new `console` type

**Recommendation: add `console` to `CertificateType`.**

The alternative, reusing `pam` with a channel flag, is cheaper by exactly
one migration and worse in every other way. `cert_options` is keyed by
certificate type (`newCertTypePolicies`,
`server/service/certtypepolicy.go:85`), so reuse means a console session and
a `sudo` invocation share one `require` condition, one `valid_duration`, one
key ID template. Operators need those separate: a
`sudo` may be approvable by a colleague while a console login is not, and a
`sudo` and a login by the same person must stay distinguishable in an audit
log, which is the reason `cert_options.pam.key_id_template` already refuses
to inherit the user template (`server/config/defaults.yaml:918-922`).

The cost is stated plainly because it is the one genuinely awkward part:

- `server/model/enums.go`: new constant, and the note there about all three
  places moving together applies.
- `server/model/certificate_request.go:29`: the `check:` tag.
- Two migrations. Postgres can `ALTER TABLE ... DROP CONSTRAINT` and re-add.
  **SQLite cannot alter a `CHECK`**, so the `certificate_requests` table must
  be rebuilt with the 12-step procedure, and two tables carry foreign keys
  into it (`server/resources/migrations/sqlite/20260101000000_init.up.sql:120`
  and `:216`), so `PRAGMA foreign_keys` handling has to be right. No
  migration in the repo has changed a `CHECK` yet, so this is the first.
  `make test-migration` (`test/migration/`, sqlite/postgres parity) is the
  check that this landed correctly, and it should be extended with a
  round-trip that inserts a `console` row on both backends.
- `frontend/src/lib/api/generated/enums.ts` regenerates via `make types`
  (tygo, see `server/model/enums.go`'s header comment).

## Wire changes

`internal/apitypes`:

```go
// ConsoleRequestBody is POST /api/certs/console. Username is the local
// account being logged into; the remaining context fields are
// self-reported by an unauthenticated caller and are displayed to the
// approver as such.
type ConsoleRequestBody struct {
    PublicKey        string           `json:"public_key" binding:"required"`
    Username         string           `json:"username" binding:"required"`
    Hostname         string           `json:"hostname,omitempty"`
    PAMService       string           `json:"pam_service,omitempty"`
    TTY              string           `json:"tty,omitempty"`
    RemoteHost       string           `json:"remote_host,omitempty"`
    RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}
```

`CreateRequestResponse` (`internal/apitypes/certrequest.go:43`) gains four
fields. The three code fields are empty for every existing type, so no
current consumer changes; `ExpiresAt` is populated for every type, which is
new information a current consumer may ignore:

```go
    // UserCode is the short code a human types into the web UI. Console
    // requests only.
    UserCode string `json:"user_code,omitempty"`
    // VerificationURL is the page that accepts UserCode, relative like the
    // other two URLs. VerificationURLComplete embeds the code.
    VerificationURL         string `json:"verification_url,omitempty"`
    VerificationURLComplete string `json:"verification_url_complete,omitempty"`
    // ExpiresAt is when this request stops being approvable, from the
    // request's own type budget. The client bounds its wait by this
    // rather than by a local guess, and displays the time remaining.
    ExpiresAt time.Time `json:"expires_at"`
```

`ExpiresAt` is what makes the per-type budget safe to configure. Without it
the module's `timeout=` and the server's `client_timeout` are two numbers an
operator has to keep in agreement by hand, and getting it wrong produces a
module still waiting on a request the server killed, reported as a generic
timeout. With it, the module waits `min(timeout, ExpiresAt)` and the console
can print a countdown that is true. RFC 8628 carries `expires_in` for the
same reason.

The same four context fields are worth adding to `PAMRequestBody` as well,
and displaying on the `sudo` approval page. That is independently useful and
can ship first (see [Sequencing](#sequencing)).

New endpoints:

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `POST` | `/api/certs/console` | none (as with the other create endpoints) | create a console request, return code, URLs and expiry |
| `POST` | `/api/certs/requests/resolve-code` | session + CSRF | body `{"code": "..."}`, claims and returns the request ID |
| `GET` | `/c/:code` | session (SPA route) | the complete-URL shortcut; resolves then redirects to `/approve/<id>` |

`resolve-code` is a `POST` despite being a lookup, for the same reason
`ClaimApprovalPage` makes a `GET` state-changing: submitting a code claims
the request.

## Schema

One migration on `certificate_requests`, both backends:

| Column | Type | Notes |
| --- | --- | --- |
| `user_code` | `TEXT NOT NULL DEFAULT ''` | the 8-symbol code, normalized form; unique among non-terminal rows |
| `hostname` | `TEXT NOT NULL DEFAULT ''` | self-reported |
| `pam_service` | `TEXT NOT NULL DEFAULT ''` | self-reported |
| `tty` | `TEXT NOT NULL DEFAULT ''` | self-reported |
| `remote_host` | `TEXT NOT NULL DEFAULT ''` | self-reported |

`hostname` is a new column rather than a reuse of `local_hostname`, whose
model comment pins it to `CertificateTypeUser`
(`server/model/certificate_request.go:46-52`); widening that field's meaning
would make the comment wrong for a saving of one column. **[judgement]**

Uniqueness on `user_code` is over live rows only. Postgres takes a partial
unique index; SQLite supports partial indexes too, so the same predicate
works on both. Bound the self-reported strings at insert (a hostname is at
most 253 bytes, a tty path far less) so an unauthenticated caller cannot
write arbitrary volume into the table, the same reasoning as
`claimUserAgentMaxLen` (`server/service/certrequest_claim.go:75`).

## Config shape

```yaml
cert_options:
  console:
    # Who may approve a console login. Same grammar as every other type.
    require:
      group: staff
    # Refuse creation from outside these networks, before anything is
    # certified. Empty means no network gate.
    allowed_networks: []
    # This type's whole budget: the longest a console login can sit waiting
    # for a human. Unset inherits cert_options.client_timeout. A value
    # LONGER than that is a startup error -- the global is the ceiling,
    # and a type may only shorten it.
    #
    # The human's share is client_timeout - 2*(client_timeout/10), so 2m
    # here gives the approver 96s, not 120s. Below about 90s a first
    # approval that has to go through an OIDC login starts to fail, and a
    # flow people habitually retry is a flow people habitually approve
    # without reading.
    client_timeout: 2m
    # Validated once and discarded, exactly like the pam type.
    valid_duration: 30s
    extensions: []
    key_id_template: ""
```

`http.rate_limit.console`, mirroring `http.rate_limit.pam`
(`server/config/types_http.go:249-251`), plus a limit on code submission
keyed by session, in the shape of the existing
`http.service_code_rate_limit` (`server/config/defaults.yaml:310-313`),
which exists for precisely this reason: *"keyed on the enrollment code to
protect against brute-forcing."*

`cert_options.client_timeout` keeps its present meaning and its 5m default,
and gains a second one: it is the ceiling every per-type budget is checked
against at startup. That is what makes this a small change rather than a
rewrite of the request-timing machinery. `SigningGrace`, `ApprovalTTL`, the
stranded-request sweep cutoff (`server/service/sweep.go:81-84`), the
resolved-outcome cache eviction (`EvictResolved`) and the sweep interval
(`server/bootstrap/scheduler.go:206-246`) all keep computing from the
global. A bound derived from the longest possible budget is still correct
for a request on a shorter one: the sweep never cancels something that
might be in flight, and the cache never evicts an entry a client could
still be waiting on. Only two things consult the type: the request's own
expiry, and what the create response reports.

`ApprovalTTL`'s doc comment currently says the value is *"shared across the
certificate types ... not a per-type concept"*. That was true and stops
being true here, so the comment moves with the code. This does not reopen
the decision against splitting the approval and signing windows
(`docs/project/decisions.md`, "Separate knobs for the approval window and
the signing grace"): the reasoning there was that an operator cares about
the total a client can hang, which is why the per-type knob is a
`client_timeout` with the same derived shares, not an `approval_ttl`.

Config documentation is generated (`genconfdocs`), so the comments above are
the documentation.

## Module arguments

Added to `docs/pam.d-sudo.example`'s companion, a new
`docs/pam.d-login.example`:

```
mode=console        selects the console flow. Absent or mode=sudo is
                    today's behaviour, unchanged. An unrecognized value
                    fails with PAM_NO_MODULE_DATA rather than falling back.

allow-root          permit mode=console for the root account. Off by
                    default: root console login is the recovery path that
                    has to keep working when ssoosshd does not.

qr=auto|on|off      whether to draw a QR code of the verification URL.
                    auto (default) draws it only when PAM_TTY names a tty,
                    the locale is UTF-8, and the terminal is at least 40
                    columns. The code and short URL are always printed
                    alongside it.

prompt=info|enter   how the code reaches the human. info (default) uses
                    PAM_TEXT_INFO and then waits on the server. enter uses
                    PAM_PROMPT_ECHO_ON, so the conversation blocks until
                    the user presses Enter and the module then checks the
                    outcome. Use enter only for a stack where PAM_TEXT_INFO
                    is verified not to render.

timeout=DURATION    a local cap on the wait, not the deadline. The module
                    waits min(timeout, expires_at-from-the-server), so a
                    stack that never sets it still expires exactly when the
                    server says, and one that sets it shorter gives the
                    terminal back sooner. Default stays 60s; the operator
                    knob that matters for console is now
                    cert_options.console.client_timeout, server-side, where
                    it cannot be edited by whoever is at the keyboard.
```

Everything else (`server`, `trusted-ca-file`, `principals-map`,
`skew-tolerance`, `insecure-skip-verify`, `debug`) is unchanged and applies
to both modes.

### The `prompt=enter` escape hatch

`Conversation` today is `Info(msg string) error` over `PAM_TEXT_INFO`
(`pam_ssoossh/conversation.go:56-62`). Whether a given stack actually
renders a multi-line `PAM_TEXT_INFO` before the module blocks is a property
of the *application's* conversation function, not of the module:
`login` via libpam's `misc_conv` writes it to the tty immediately, while
other applications batch messages until the next prompt. **This must be
measured per service during rollout, not assumed** (see
[Testing](#testing)).

`prompt=enter` exists for whatever fails that measurement. It puts the code
in a `PAM_PROMPT_ECHO_ON` prompt, which guarantees the text is delivered
because the application has to render it to collect the answer, and it gives
the user an explicit "I approved it" signal. Its cost is an extra keystroke
and that the module cannot react to an approval until Enter arrives, so it
is not the default.

## Web UI

Two additions to `frontend/src/routes`:

- **`/console`**: a code entry box. Deliberately minimal, and reachable from
  the dashboard so a signed-in user can get to it without transcribing the
  URL from the console. Normalizes input as it is typed (uppercase,
  auto-hyphen after four symbols), submits to `resolve-code`, redirects to
  `/approve/<id>`. Distinguishes "no such code", "already claimed by another
  session", and "expired", because those three send the user to three
  different next actions.
- **`/c/[code]`**: resolves and redirects, or renders the same three states.

The existing `/approve/[id]` page grows a console section rendering the
context table above, with the self-reported fields visibly marked as such
and a warning when `remote_host` is non-empty on a request claiming to be a
console login. `ApprovalView` already branches per certificate type, so this
is a new branch rather than new plumbing.

## PAM stack placement and lockout safety

This is the part that breaks machines if it is wrong, and it deserves more
runbook attention than the `sudo` case did.

```
# /etc/pam.d/login  (Debian/Ubuntu shown; the same idea on the RPM side)

# Who may console into THIS host. Host-side, root-owned, and it fails
# before any network call. This is the per-host gate; the server has no
# say in it and should not.
auth  [success=ignore default=die]  pam_succeed_if.so  user ingroup console-web01 quiet

auth  sufficient  pam_ssoossh.so  mode=console \
        server=https://ssoosshd.example.com \
        trusted-ca-file=/etc/ssoossh/ca.pub \
        principals-map=/etc/ssoossh/principals.yaml
auth  include     common-auth
```

Non-negotiable rules for the runbook:

0. **Put the group gate above the module, not on the wire.** See
   [Per-host gating is the PAM stack's job](#per-host-gating-is-the-pam-stacks-job-not-a-wire-field).
   An account that may not console into this host should never reach
   `pam_ssoossh.so`, never create a request row, and never make a human's
   phone light up.
1. **`sufficient`, never `required` or `requisite`.** A ssoosshd outage must
   fall through to the local stack, not lock the console.
   `classifyRequestError` already returns `PAM_AUTHINFO_UNAVAIL` for an
   unreachable server (`pam_ssoossh/auth.go:163-172`), which is the code
   that lets the stack continue.
2. **Keep a working local credential**, and keep it somewhere physical. A
   console behind an SSO that needs the network is a console that does not
   work when the network is the thing that is broken.
3. **Never edit `/etc/pam.d/login` without a second root session open**, and
   verify from that session before closing it. This is the standard PAM
   rule, and it matters more here because the failure is at the physical
   console.
4. **`root` stays out** unless `allow-root` is deliberately set.
5. **Screen lockers are the same stack.** `sddm`, `swaylock`, `xscreensaver`
   and friends authenticate through PAM, so adding the module to a shared
   `common-auth` puts screen unlock behind the network. That is a lockout
   waiting to happen on a laptop. Wire it per service, not into
   `common-auth`.
6. **Accounts must already exist.** ssoossh provisions nothing: the account
   has to resolve through NSS (local `/etc/passwd`, SSSD, LDAP) before
   `login` will even offer it a PAM stack. Pair with `principals-map` for
   the identity-to-account mapping.

## Decisions, and the reasoning behind each

**One `.so` with a mode, not a second module.** The four security checks
must never diverge between the two flows, and a shared binary makes that
structural rather than a review discipline. The packaging cost of a second
module is real but secondary to that.

**A new certificate type despite the SQLite migration cost.** Operators need
independent `require`, lifetime, and key ID for a session versus a
command. The migration is a one-time cost with an existing parity test
(`make test-migration`) to catch it going wrong.

**No approver-to-account linkage rule.** It was in an earlier draft of this
document, on the reasoning that an approver should be forced to hold the
account being logged into. Two things kill it. Against the phone-call
attack it does nothing: the attacker names the victim's own account, so the
check passes. And after
[pam-principal-source.md](pam-principal-source.md) it is structurally
redundant, the principals *are* the approver's held accounts, so there is
nothing left to cross-check. `linkage` stays nil for both PAM and console,
correctly this time.

**Per-host group gating stays in the PAM stack.** A server-wide `require`
condition does not scale past a homelab, but the fix is `pam_succeed_if`
above the ssoossh line, not a group field on the request. Host-side, it is
root-owned, cannot be omitted by whoever is at the console, costs no network
call, and is already how every fleet configures PAM. On the wire it would be
untrusted input that silently stops applying the moment someone attacks it.

**Source network, not hostname, for the server's half of the per-host
gate.** The design brief says "per-host", but a host cannot prove its name,
and the project already declined host certificates on exactly that ground.
The source address is the strongest signal that actually exists. This is a
correction to the brief, not a reinterpretation of it.

**Codes resolve only for an authenticated session.** Anything else turns 40
bits into an unauthenticated path to the request ID, and the request ID
yields the certificate.

**Refuse `root` by default.** **[judgement]** Cheap, host-side, no round
trip, and it protects the recovery path that the whole design otherwise
depends on.

**The approval deadline is the server's, and it is per type.** An earlier
draft made this a longer module-side `timeout=` on the reasoning that the
human is walking to a phone. That is backwards on both counts. The window is
the attacker's working time in the consent-phishing case, so console wants a
*shorter* one, not a longer one; and a deadline the host sets is a deadline
whoever is at the host can change. The server owns it
(`cert_options.console.client_timeout`, default 2m against a 5m ceiling),
reports it as `expires_at`, and the module's `timeout=` degrades to a local
cap. The global stays the ceiling so every bound derived from it
(`sweep.go`, `EvictResolved`, the sweep interval) keeps working untouched.

**QR rendered in the module, not the server.** Server-rendered ANSI art is
bytes from the network written to a tty, which is an escape-sequence
injection primitive. The dependency cost of an encoder is the smaller risk,
and character whitelisting applies either way.

## Deliberately rejected or deferred

- **Offline console login (working with ssoosshd unreachable).** This is the
  most-requested thing this design does not do, and it is worth stating why.
  Anything that authenticates without reaching the server needs a
  pre-shared secret on the host (an HOTP/TOTP seed, a per-host key for a
  challenge-response), because a bare CA signature over a challenge is 64+
  bytes and no human types 103 base32 characters at a console. A
  pre-shared per-host secret is a long-lived credential sitting on the
  target, which is the exact thing this project exists to remove. The
  answer stays: keep `pam_unix` in the stack as the documented break-glass
  path, and treat the local password as a physical-security control.
- **Number matching** (console shows a digit pair, the web UI offers three
  choices, the approver picks the match), as Entra does for MFA fatigue.
  The fatigue it defends against does not exist here: approvals are rare,
  deliberate, and already require typing a code that only exists on the
  console screen. It adds a step to every login to defend against a
  pattern the flow does not produce.
- **An approver-group field sent by the module and enforced server-side.**
  The motivating problem is real: `cert_options.<type>.require` is one
  condition for a whole deployment, and in a fleet it degenerates to the
  union of every team. But the request is created by an unauthenticated
  caller, so the field could only narrow (`require` AND the requested
  group), and the attack is not to widen it but to **omit** it and fall back
  to the server-wide condition. A control that silently stops applying when
  someone attacks it is worse than no control, because the operator believes
  it is there. `pam_succeed_if.so user ingroup ...` above the ssoossh line
  does the same job host-side, root-owned, before any network call. See
  [Per-host gating is the PAM stack's job](#per-host-gating-is-the-pam-stacks-job-not-a-wire-field).
- **TOTP/HOTP as a second factor at the console.** That is `pam_oath` or
  `pam_google_authenticator`, it is not SSO, and stacking it is an operator
  decision, not a feature of this module.
- **Push notification as the default channel.** See
  [channel 4](#4-push-to-the-users-registered-device-deferred). Deferred
  rather than rejected.
- **Provisioning local accounts.** Out of scope, and it would make the
  module a system-configuration tool rather than an authenticator. NSS
  already solves it.
- **Session re-authentication or lifetime enforcement on the session.** The
  certificate authorizes the login and is discarded; the session then lives
  as long as the OS lets it. Bounding that is `pam_sm_open_session` plus a
  session reaper, a separate feature with its own failure modes.
- **`pam_sm_acct_mgmt` / `pam_sm_open_session` in this module.** Still
  `auth` only. `docs/pam.d-sudo.example` already tells operators not to add
  the module to other management groups, and that stays true.

## Sequencing

Each phase is independently shippable and independently useful.

**Phase 0: approval context for PAM requests.** The four context fields on
`PAMRequestBody`, the columns, `PAM_SERVICE`/`PAM_TTY`/`PAM_RHOST` read via
`pam_get_item` in the module, and the display on `/approve/[id]`. No new
certificate type, no new endpoint. Improves the shipped `sudo` flow on its
own and removes the largest chunk of schema and UI work from Phase 1.

**Phase 1: the console type and the typed-code flow.** The enum, model tag,
and both migrations; `cert_options.console` including its own
`client_timeout`; `POST /api/certs/console`;
`resolve-code`; the `/console` page; `mode=console`, `allow-root`, and the
console `timeout` default in the module. This is a complete, usable feature.

**Phase 2: fewer keystrokes.** `verification_url_complete`, the `/c/:code`
route, and QR rendering with `qr=auto`. Pure UX on top of a working Phase 1.

**Phase 3: gating and reach.** `allowed_networks` refusal at creation, and
then, if wanted, the opt-in push notification.

`prompt=enter` lands in whichever phase the per-service rendering matrix
(below) shows it is needed for.

## Testing

Following `.claude/rules/test-go.md` and `.claude/rules/go.md`.

**Unit, table-driven:**

- Code alphabet: encode/decode round trip, Crockford alias normalization
  (`I`/`l` to `1`, `O` to `0`), hyphen and whitespace stripping, case
  folding, rejection of out-of-alphabet symbols, empty input, wrong length.
- `parseArgs`: `mode` absent, `sudo`, `console`, unrecognized (fails, does
  not fall back), `allow-root` bare and `=true`/`=false`, `qr` three values
  plus garbage, `prompt` both values plus garbage, the console `timeout`
  default applying only in console mode.
- Mode routing in `Authenticate`: console mode calls the console endpoint
  and renders code plus URL; sudo mode is byte-for-byte what it is today.
- Wait bounding: `min(timeout, expires_at)` with the server shorter, with
  the module shorter, with `expires_at` absent (an older server), and with
  an `expires_at` already in the past.
- `root` refusal: rejected before any keypair generation or network call,
  and permitted with `allow-root`.
- QR rendering: known-answer test against a fixed URL, and a character
  whitelist assertion that nothing outside the block characters, space, and
  newline can reach the conversation.
- Server: code uniqueness among live rows, code expiry with the request,
  `resolve-code` refusing an unauthenticated caller, refusing a second
  session, and refusing a terminal request; `allowed_networks` refusing at
  creation rather than approval; a per-type `client_timeout` longer than
  the global ceiling refused at startup, and a request expiring on its own
  type's budget rather than the global one.
- Frontend (`.claude/rules/test-ts.md`): `/console` normalization as typed,
  and the three distinct failure states.

**Migration:** extend `test/migration/` so the sqlite/postgres parity run
inserts and reads back a `console` row, which is what proves the SQLite
table rebuild kept the foreign keys and the other constraints.

**End to end:** extend `test/e2e/pam_stack_test.go`. The existing harness
already installs a dedicated `/etc/pam.d` service and drives a real
`pam_authenticate` through `pamtest.c`, so a console-mode case is a new
service file plus a code-submission step against the real ssoosshd, not new
infrastructure. Assert that the certificate is issued only after the code is
submitted and approved, and that a wrong code never produces one.

**The rendering matrix, measured not assumed.** Before Phase 1 ships,
record for each service whether a multi-line `PAM_TEXT_INFO` is displayed
before the module blocks, and how many columns are usable:

| Service | Multi-line `PAM_TEXT_INFO` visible? | Usable width | QR viable? |
| --- | --- | --- | --- |
| `agetty` + `login` (text console) | | | |
| serial console | | | |
| BMC/iDRAC HTML5 console | | | |
| Proxmox / vSphere VM console | | | |
| `gdm` | | | |
| `sddm` | | | |
| `lightdm` | | | |
| `sshd` keyboard-interactive | | | |

The `qr=auto` heuristic and the existence of `prompt=enter` are both driven
by what this table says. It belongs in
[docs/dev/cross-platform-testing.md](../dev/cross-platform-testing.md) once
filled in.

## Provenance: what was verified and how

Read in full at `a009511`: `pam_ssoossh/*.go`, `pam_ssoossh/pam.go`,
`pam_ssoossh/conversation.go`, `docs/pam.d-sudo.example`,
`server/service/certtypepolicy.go`, `server/model/enums.go`,
`server/model/certificate_request.go`, `docs/project/decisions.md`,
`docs/internals/design-brief.md`.

Read in part: `server/controller/certrequests.go` (route registration and
the PAM/events handlers), `server/service/certrequest.go`
(`checkApproverAuthorization`, `checkUserPrincipalLinkage`,
`ApprovalSelection`, `RequestDetail`, the enrollment-token region),
`server/service/certrequest_claim.go` (claim semantics),
`internal/apitypes/apitypes.go` (wire bodies), `server/config/defaults.yaml`
(`cert_options.pam`, `client_timeout`, rate limits), `Makefile` (pam, test,
migration targets), `.goreleaser.yml` (pam build ids and nfpm contents),
`frontend/src/routes/approve/[id]/+page.svelte`.

Specific claims and how each was checked:

- *"a PAM certificate takes the local account, not the approver"*:
  `grep -n "policy.principals" server/service/certrequest.go` gives the two
  call sites (`:525`, `:1192`), both passing `req.Username` first; the PAM
  entry at `server/service/certtypepolicy.go:145-147` discards `identity`
  and the selection. `model.CertificateRequest.Username`'s comment says the
  same thing in prose.
- *"the consent-phishing gap is recorded"*: `bindRequester`'s doc comment,
  `server/service/certrequest.go:721-725`. The
  `docs/security-review-2026-08-11.md` it cites does not exist in the tree
  (`grep -rn security-review-2026-08-11` finds only that comment).
- *"ApprovalTTL is a fixed fraction of one global budget"*:
  `server/config/types_certificates.go:39-61`, and the consumers are
  `server/service/sweep.go:81-84`, `EvictResolved`
  (`server/service/certrequest.go:415-440`) and
  `server/bootstrap/scheduler.go:206-246`.
- *"the type is a CHECK in three places"*: `grep -rn
  chk_certificate_requests_type server/resources/migrations` returns the two
  init files at line 56; the model tag is
  `server/model/certificate_request.go:29`.
- *"no migration has changed a CHECK yet"*: `grep -ln CHECK
  server/resources/migrations/sqlite/*.sql` returns only the init and the
  LDAP-enrichment files.
- *"two tables reference `certificate_requests`"*: `grep -n "REFERENCES
  certificate_requests" server/resources/migrations/sqlite/*.sql`.
- *"the module has no QR or OTP dependency today"*: `grep -niE
  "qrcode|totp|otp" go.mod` returns nothing.
- *"the module reads no PAM items but `PAM_CONV`"*: `grep -n pam_get_item
  pam_ssoossh/*.go` returns only `conversation.go:16`.
- Not verified, and flagged as such above: how each PAM application renders
  a multi-line `PAM_TEXT_INFO`. That is the rendering matrix, and it is
  measurement, not reading.

## Open questions

1. **A group key in `principals-map`.** The one case the PAM-stack gate does
   not reach is a host with no directory-backed NSS groups, where
   `pam_succeed_if user ingroup` has nothing to resolve and `principals-map`
   has to enumerate accounts one at a time. A group key resolved against
   local groups would close it, host-side, without putting anything on the
   wire. Worth doing on its own merits; not a blocker for this design.
2. **What the console `client_timeout` default should actually be.** 120s
   is proposed below on reasoning, not measurement. The number that matters
   is how long a first approval takes when the approver is not already
   signed in: OIDC plus MFA can be most of a minute on its own. Time it
   before picking.
3. **Whether a shared operator account needs anything.** Several people
   legitimately logging into one console account is a `principals-map` and
   NSS-group question, host-side, and LDAP enrichment
   (`docs/operations/ldap.md`) is parsed but not yet consumed. Worth
   confirming nothing server-side is needed once that lands.
5. **Which QR encoder**, and whether vendoring a minimal one is preferable
   to a dependency, given this is a `.so` loaded into every authenticating
   process.
6. **Where the `/console` entry point lives in the UI** so that a user who
   has never used it can find it without transcribing a URL. Dashboard tile,
   nav item, or both.
7. **Whether Phase 0's context fields should also be sent by `ssoossh ssh
   login`** for user certificates, which already send `local_username` and
   `local_hostname` but no tty or service. Probably yes, for consistency of
   the approval page, but it is not needed by this design.
