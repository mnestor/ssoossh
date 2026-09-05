# Service certificate source-address restrictions

**Status: designed, nothing built.** No code has been written for this. Every
`file:line` anchor below was verified against `5d23809` (2026-08-24) and will
drift.

> **Before planning from this document**, re-run the verification pass in
> [Provenance](#provenance-what-was-verified-and-how), and read the reasoning
> in [Decisions](#decisions-and-the-reasoning-behind-each) rather than only
> the decisions. The ones resting on judgement about operator behaviour, not
> on facts about the code, are flagged **[judgement]** and are the ones worth
> re-opening first.

Related documents, neither of which this one edits:

- [Certificate policy](https://mnestor.github.io/ssoossh/operations/certificate-policy/) owns the
  lifetime engine and records, under "Known defect: `source-address` is welded
  to the lifetime rule", why the current pinning implementation needs rework
  rather than extension. This proposal is the answer to the five questions
  that section leaves open, and supersedes `pin_source_address`. That document
  is left untouched; see
  [Coordination with the lifetime policy engine](#coordination-with-the-lifetime-policy-engine).
- [service-retrieval-anomaly-policy.md](service-retrieval-anomaly-policy.md)
  owns detection of a leaked enrollment code by counting distinct redemption
  addresses. The two designs touch the same function, the same table, and the
  same notification registry. See
  [Coordination with the anomaly policy](#coordination-with-the-anomaly-policy).

## What this proposes

Two independent restrictions on a service enrollment, both chosen by the
approving human at approval time, both bounded by server configuration:

1. A **certificate pin**: the `source-address` critical option, written into
   every certificate the enrollment produces, enforced by the sshd on every
   host the certificate is later presented to.
2. A **retrieval gate**: an allowlist of networks that may redeem the
   enrollment code, enforced by ssoosshd at `service retrieve`, invisible to
   the certificate.

Plus the client-side and UI work that makes either usable: `service enroll`
reporting the host's own addresses, those addresses carrying their prefix
length, and an approval screen that offers them as candidates with a widening
control bounded by an administrator's ceiling.

The shape of the control in both cases is the same. The client claims a set of
addresses, the server observes one, the approver picks from those candidates
and may widen each to a larger network, and configuration bounds how far.

## The two mechanisms

They are frequently confused, so the differences are worth stating flatly
before anything else.

| | certificate pin | retrieval gate |
| --- | --- | --- |
| Enforced by | the target host's sshd | ssoosshd, at redemption |
| Checked against | the address the client connects to *the target* from | the address ssoosshd observes on the retrieve call |
| Correct candidates | the client's own interface addresses | the server-observed source IP |
| Present in the certificate | yes, as `source-address` | no |
| Changeable after approval | no, frozen in every issued certificate | yes, it is server-side state |
| Failure mode | opaque sshd rejection at connect time | refusal at redemption, notifiable |
| Holds when ssoosshd is down or bypassed | yes | no |
| Protects | use of an issued certificate | issuance of new certificates |

### Why both, rather than one

The gate is the better default for most deployments. It is server-side state,
so it can be tightened or relaxed after the fact without re-enrolling; it
fails at a point where the server can say why and tell somebody; and it does
not care about NAT, because the value it checks is produced at the same
observation point that checks it.

The pin is strictly stronger where it applies, because it does not depend on
ssoosshd being in the path at all. A certificate already minted, or a code
redeemed from an allowed address, is entirely outside the gate's reach. Only
the pin constrains what happens after issuance.

Neither subsumes the other, so both are offered, independently, and a
deployment can configure either, both, or neither.

## What exists today (verified)

| Piece | Where | State |
| --- | --- | --- |
| Client reports interface addresses | `client/cmd/ssh_login.go:306`, `auth.go:69` in github.com/mnestor/ssoossh-pam | `api.LocalInterfaceAddresses()`, `ssh login` and PAM only |
| `service enroll` reports nothing | `client/cmd/service_enroll.go:73` | Sends `api.RequestedOptions{}` |
| Interface prefix length discarded | `internal/api/localaddrs.go:65` | `ipNet.IP.String()`, the mask is in hand and dropped |
| Claimed addresses normalized and stored | `server/service/certrequest.go:302-305` | Link-local dropped, deduped, observed `SourceIP` unioned in |
| Approval screen shows them | `frontend/src/lib/components/ApprovalView.svelte:173-181` | "Registered IPs", read-only chips |
| Observed address shown separately | `ApprovalView.svelte:163-165` | "Requested from", from `detail.source_ip` |
| Requested/granted pair on the wire | `server/webtypes/webtypes.go:207-212, 240` | `CertificateOptionsResponse.SourceAddresses` on both sides |
| Approver's choices on the wire | `server/webtypes/webtypes.go:94-97` | `ApproveRequestBody{ServiceAccount, Principals}` |
| Approve drops the claimed set | `server/service/certtypepolicy.go:68-73` | `narrowRequestedOptions` returns only extensions and no-touch |
| Lifetime engine re-adds a pin | `server/service/lifetimepolicy.go:305-307` | `narrowed.SourceAddresses = []string{sourceIP}` when the winning rule sets `pin_source_address` |
| Applied to service enrollments | `server/service/certrequest.go:727` | `approveServiceEnrollment` calls it after `narrowRequestedOptions` |
| Signer emits the option | `server/signer/sign.go:99-106` | `strings.Join(opts.SourceAddresses, ",")` into `source-address` |
| Redemption source address | `server/controller/enrollment.go:60` | `g.ClientIP()`, subject to `http.trusted_proxies` |
| Redemption path | `server/service/enrollment.go:94` | Row written at `:148`, signing job published at `:185` |
| Expired code answers `NotFound` | `server/service/enrollment.go:104-108` | Deliberate: an expired code is indistinguishable from an unknown one on the wire |
| Retrieval row shape | `server/model/enrollment.go:62-81` | `source_ip`, `certificate_serial`, `succeeded` |
| Notification registry | `server/notify/notify.go:72-76` | Appending a `Definition` adds a kind |
| Notification queueing | `server/service/notification.go:76` | Never blocks the caller, never fails the operation |
| User certificates are not pinned | `docs/decisions.md:87-92` | Decided: people move, services sit still |

Three facts from that table drive most of what follows:

1. **The claimed addresses already reach the approval screen.** The plumbing
   for the pin exists end to end and is severed at exactly one point, the
   drop in `narrowRequestedOptions`.
2. **The signer turns `RequestedOptions.SourceAddresses` into a certificate
   option.** Anything stored there becomes part of the certificate. The
   retrieval gate therefore cannot be stored there.
3. **`service enroll` sends no addresses at all**, so the flow this feature
   exists for is the one flow with no claimed candidates today.

## The candidate asymmetry

This is the part most likely to be got wrong in implementation, because the
two mechanisms take a CIDR from the same screen and look interchangeable.

**The pin is enforced somewhere ssoosshd never sees.** A certificate carrying
`source-address` is checked by the target host against the address the client
connects to *it* from. Under NAT that is the service host's own address, not
the NAT egress that ssoosshd observed. So the pin's candidates are the
client's claimed interface addresses, and the observed address is usually the
wrong choice.

**The gate is enforced at the same point that produced its input.** It
compares `g.ClientIP()` at redemption against networks chosen from
`g.ClientIP()` at enrollment. Both are the same observation, so they agree by
construction. The client's claimed `10.1.1.5` is actively wrong here: choose
it when ssoosshd observed `203.0.113.9`, and the legitimate job is locked out
on its next run.

Consequences:

- The approval screen shows **two pickers with different default candidate
  sets**. The pin picker defaults to the claimed interface addresses; the
  gate picker defaults to the observed address.
- Neither picker forbids the other's candidates. A service host with a public
  address and no NAT has one address that is legitimately both, and a NAT
  egress is a legitimate pin target for a deployment that wants exactly that.
  The default is the guidance; the restriction is the ceiling.
- The gate has the stronger trust story, because its candidate is a fact
  ssoosshd established rather than a claim the client made. Its
  `allowed_networks` bound is defense in depth. The pin's ceiling is the thing
  actually holding the narrowing invariant up.

`CreateRequest` unions the observed address into the claimed list before
storing it (`certrequest.go:303-305`), so the stored set carries no provenance
marker. The UI recovers it: `RequestDetailResponse.SourceIP` is the observed
address, and the entry matching it is the observed one. Provenance is shown on
each chip, because an approver choosing between "the host says it has this
address" and "we saw this address" is making a different decision in each
case.

## The validation rule

One rule does most of the work. **A submitted network must contain a candidate
address from the request.**

For each network `P` the approver submits, on either axis, the server
re-derives, from stored state and configuration only:

1. `P` parses as a prefix.
2. Some candidate `C` satisfies `P.Contains(C)`, where the candidate set is
   the axis's own (claimed addresses plus observed for the pin, observed for
   the gate).
3. `P.Bits() >= max_expansion` for `P`'s address family.
4. If `allowed_networks` is non-empty for that axis, some entry `N` satisfies
   `N.Contains(P)`.

`10.1.1.5` widens to `10.1.1.0/24`. It cannot become `10.2.0.0/16`, because
`10.1.1.5` is not in that network. Rule 2 is self-enforcing and needs no
separate notion of a legitimate expansion: widening is only ever anchored to
something the request actually carried.

Rules 3 and 4 are the two administrator knobs and they are genuinely
independent. Rule 3 bounds *how far* a single candidate may be widened. Rule 4
bounds *where* a network may sit at all, regardless of how it was reached.

### Why a lying client cannot escalate

The claimed address list is client-supplied and unverified, and that is stated
as a hard boundary in the lifetime policy document. It is not violated here,
because the claimed list decides only what the approver is *shown*.

A client that claims `8.8.8.8` puts `8.8.8.8` on the approval screen. If an
approver selects it, the resulting pin is bounded by rule 3 (it cannot be
widened past the ceiling) and by rule 4 (it must sit inside an administrator's
network, and `allowed_networks` is where a deployment says "internal ranges
only"). More fundamentally, every outcome on the pin axis is *more*
restrictive than the status quo, in which the option is dropped and the
certificate carries no address restriction at all. There is no selection that
yields a more capable certificate than today's.

The gate axis does not have even that exposure, because its candidate is the
observed address.

### Collapse, bounds, and ordering

- **Overlapping selections collapse.** Two candidates on the same LAN widened
  to the same `/24` are one entry, not two. Collapse happens in the server
  validator, not only in the UI, because the UI is not the only caller of the
  approve endpoint.
- **`max_entries` bounds the result** on both axes. On the pin axis this keeps
  the joined critical option from growing without limit, since
  `sign.go:104-105` concatenates the list into one string with no bound of its
  own. On the gate axis it bounds the per-redemption match loop.
- **Entries are stored sorted**, so the same selection produces the same
  stored value and the same certificate option, and a diff between two
  enrollments is readable.

### IPv6

`max_expansion.ipv6` defaults to **64**, not 128. A host with SLAAC privacy
extensions rotates through addresses inside its own `/64` as routine
behaviour, so a `/128` pin breaks within a day and a `/128` gate entry breaks
on the next rotation. The `/64` is the honest equivalent of the `/24` in the
IPv4 case: one link, one host's worth of addresses.

This is the same reasoning, and the same default, that the anomaly policy's
normalization section reaches for its own counting unit. The two are not the
same mechanism and must not be unified: the anomaly detector masks addresses
before counting them, and this feature compares an address against
approver-chosen networks without masking anything.

## Config shape

Global, under the existing service block, alongside the other certificate
policy. The same shape appears twice, once per axis, deliberately: one mental
model, two enforcement points.

```yaml
cert_options:
  service:
    enrollment_duration: 8760h
    valid_duration: 12h

    source_address:
      # Writes the source-address critical option into every certificate this
      # enrollment produces. Enforced by the target host, not by ssoosshd.
      pin:
        # required - the approver must select at least one network
        # optional - the approver may select none
        # disabled - the picker is not shown; nothing is ever pinned
        mode: disabled

        # How far a single candidate address may be widened. A candidate is a
        # /32 or /128 as it arrives; these are the widest it may become.
        max_expansion:
          ipv4: 24
          ipv6: 64

        # Networks a selection must sit inside, whatever it was widened from.
        # Empty means no such bound; the expansion ceiling still applies.
        allowed_networks: []

        # Upper bound on selected networks, after overlap collapse.
        max_entries: 8

      # Gates `service retrieve`. Enforced by ssoosshd. Never appears in a
      # certificate.
      retrieval:
        mode: disabled
        max_expansion:
          ipv4: 24
          ipv6: 64
        allowed_networks: []
        max_entries: 8

        # Mail the enrollment owner when a redemption is refused on address
        # grounds. See "Notification" below: coalesced, never one per attempt.
        notify_on_refused: true
        notify_cooldown: 1h
```

Both axes default to `disabled`. An upgrade must not begin pinning
certificates or refusing redemptions for enrollments approved under rules
nobody saw.

### Startup validation

Alongside the existing `enrollment_duration` check
(`server/config/types_certificates.go:85`):

- `mode` is one of the three spellings, on each axis.
- `max_expansion.ipv4` in 8..32 and `max_expansion.ipv6` in 32..128, on each
  axis where mode is not `disabled`. The lower bounds are what make a `/0`
  selection unrepresentable rather than merely discouraged, which answers open
  question 5 of the lifetime document's rework list for this mechanism.
- Every `allowed_networks` entry parses as a CIDR, and its own prefix length
  is not wider than the corresponding `max_expansion`. A `10.0.0.0/8` entry
  under a `/24` ceiling is coherent (it bounds where, not how far); an
  `allowed_networks` of `0.0.0.0/0` with a `/24` ceiling is also coherent and
  simply means "no positional bound". Both are accepted.
- `max_entries >= 1`.
- `notify_cooldown >= 0`; zero means every refusal mails, which is allowed and
  documented as noisy.
- **A startup error**, not a warning, when `pin.mode` is not `disabled` and
  any `lifetime_policy.source_policy` entry sets `pin_source_address: true`.
  See [Coordination with the lifetime policy engine](#coordination-with-the-lifetime-policy-engine).
- **A warning**, not an error, when `retrieval.notify_on_refused` is true and
  `mail.enabled` is false. The gate still refuses; nobody is told.

## Where the choice is stored

The two axes are stored differently, and the difference is the design, not an
inconsistency.

**The pin goes in `enrollments.option_set`**, in the existing
`RequestedOptions.SourceAddresses` field. That field is what
`EnrollmentService.Retrieve` decodes at `enrollment.go:118-121` and hands to
the signing job at `:174`, and what the signer turns into the critical option
at `sign.go:104`. A pin is a certificate option, so it belongs with the
certificate options, and no new plumbing is needed to make it reach the
certificate.

**The gate goes in a new `enrollments.retrieval_networks` column**, JSON, a
sorted `[]string` of CIDRs. It must not go in `option_set`, precisely because
everything in `option_set` becomes part of the certificate. Storing the gate
there would write the operator's redemption allowlist into the
`source-address` option of every certificate, which is both wrong and the
opposite of what "no source-address in the cert" asks for.

Both are frozen at approval, consistent with the evaluate-at-enrollment-time
contract that governs principals, key ID, and certificate duration
(`server/model/enrollment.go:19-23`). Nothing is re-derived at redemption.

## Where it runs

### Approval

Inside `CertRequestService.Approve` and `approveServiceEnrollment`:

1. `narrowRequestedOptions` (`certtypepolicy.go:68`) stops discarding
   `SourceAddresses` unconditionally. It now takes the approver's selection
   and returns the validated pin, or an empty list when the pin axis is
   `disabled` or nothing was selected.
2. Validation is the four-rule check above, run server-side against stored
   request state and loaded configuration. The submitted values are never
   trusted, and the UI's clamping is convenience only.
3. `mode: required` on an axis fails the approval, with a message naming the
   axis, when the selection for it is empty. This is an approval-time rule
   only. See [Required mode governs approval, never redemption](#required-mode-governs-approval-never-redemption).
4. The validated gate networks are written to `retrieval_networks` in the same
   `Enrollment` insert that already writes `option_set`, `key_id`, and
   `principals`.

### Redemption

Inside `EnrollmentService.Retrieve`, which currently runs: load, expiry check
(`:104`), decode principals (`:111`), decode options (`:118`), compute
`validBefore` (`:134`), allocate serial (`:139`), write the retrieval row
(`:148`), subscribe (`:163`), publish (`:185`).

The gate slots in like this:

1. Load the enrollment; expiry check, unchanged (`:104-108`).
2. Decode principals and options, unchanged. Decode `retrieval_networks`.
3. **New:** evaluate the gate. `refused` is true when `retrieval_networks` is
   non-empty and the observed address is inside none of them.
4. Allocate the serial only when not refused. A refused attempt will never
   produce a certificate, and burning a serial on it would put a number in the
   audit chain that no certificate ever carries.
5. Write the retrieval row, once, as today, with `outcome` set and
   `certificate_serial` left zero when refused.
6. **New (or later):** the anomaly window evaluation, which now sees the row
   written in step 5. See
   [Coordination with the anomaly policy](#coordination-with-the-anomaly-policy).
7. If refused: queue the notification, subject to coalescing, and return
   `NotFoundError`.
8. Otherwise subscribe, publish, deliver, notify as today.

The row is written before the refusal returns, in step 5, because a refused
attempt is the single most interesting thing in the retrieval log. It is
evidence that somebody holds the code and is not where the code is supposed to
be used from.

### A refused redemption answers `NotFound`

Same answer an expired code gives, for a reason `enrollment.go:105-107` already
states about expiry, and which is stronger here.

An attacker holding a stolen code and private key, probing from a sequence of
addresses, must not be able to tell "this code is real but your address is
wrong" apart from "no such code". The first tells them the code is live and
worth continuing to attack, and tells them precisely what needs to be routed
around. The distinction is visible to the approver in the retrieval log and in
the notification, and nowhere on the wire.

## Wire changes

`ApproveRequestBody` (`webtypes.go:94`) gains two fields, symmetric with the
`ServiceAccount` and `Principals` choices already there:

```go
type ApproveRequestBody struct {
    ServiceAccount    string   `json:"service_account,omitempty"`
    Principals        []string `json:"principals,omitempty"`
    SourceAddressPin  []string `json:"source_address_pin,omitempty"`  // NEW
    RetrievalNetworks []string `json:"retrieval_networks,omitempty"`  // NEW
}
```

`service.ApprovalSelection` (`certrequest.go:67`) gains the matching pair, and
the controller copies them across at `certrequests.go:366-369` as it does the
existing two.

`RequestDetailResponse` gains the policy the UI needs in order to render and
clamp the pickers. The server re-validates everything regardless; this exists
so the screen can show a correct control and explain a rejection before it
happens rather than after:

```go
type SourceAddressAxisResponse struct {
    Mode             string   `json:"mode"`
    MaxExpansionIPv4 int      `json:"max_expansion_ipv4"`
    MaxExpansionIPv6 int      `json:"max_expansion_ipv6"`
    AllowedNetworks  []string `json:"allowed_networks"`
    MaxEntries       int      `json:"max_entries"`
}

type SourceAddressPolicyResponse struct {
    Pin       SourceAddressAxisResponse `json:"pin"`
    Retrieval SourceAddressAxisResponse `json:"retrieval"`
}
```

Present on the detail response for service-type requests only. It exposes
`allowed_networks` to a browser, which is a small disclosure of internal
topology to an authenticated session that is already trusted to approve
certificates for the deployment. Judged acceptable; noted because it is the
only new outbound configuration detail.

`ServiceEnrollmentResponse` (`webtypes.go:140`) gains `retrieval_networks`, so
the service codes page can show why a job is being refused. The pin is already
visible there through the option set.

### Client

`api.LocalInterfaceAddresses` (`internal/api/localaddrs.go:33`) returns
`ip/prefix` rather than a bare address. The mask is already in hand at `:65`
(`ipNet` is a `*net.IPNet`) and is currently discarded. Sending it lets the
approval screen default each candidate's widening control to the host's actual
link rather than making the approver guess whether the site is a `/24` or a
`/22`.

The field stays `[]string`, and `normalizeSourceAddresses`
(`certrequest.go:259`) accepts both forms, so an older client that sends bare
addresses keeps working with no special case beyond parsing.

`service enroll` (`client/cmd/service_enroll.go:73`) sends
`api.RequestedOptions{SourceAddresses: api.LocalInterfaceAddresses()}` instead
of an empty set. This is the change that makes the pin usable at all for
service certificates: without it the only candidate is the observed address,
which under NAT is the wrong one.

## Schema

One migration per driver, matching the existing pair layout in
`server/resources/migrations/{postgres,sqlite}/`:

```sql
ALTER TABLE enrollments ADD COLUMN retrieval_networks TEXT NOT NULL DEFAULT '';
ALTER TABLE enrollment_retrievals ADD COLUMN outcome TEXT NOT NULL DEFAULT '';
```

`retrieval_networks` is a JSON-encoded sorted `[]string`, matching how
`principals` is stored on the same table
(`migrations/postgres/20260101000000_init.up.sql:206`). Empty string means no
gate, which is what every existing row gets on upgrade, which is what keeps
every existing enrollment working.

`outcome` records why a retrieval row exists. Today `succeeded`
(`...init.up.sql:238`) carries one bit that means "the certificate was
delivered", and its false value means "passed code validation, failed at
signing" (`server/model/enrollment.go:76-79`). A source refusal is a third
state, and the approver needs to tell "the signer broke" apart from "somebody
redeemed this from the wrong place". Values: `''` for rows written before this
column existed, `succeeded`, `sign_failed`, `refused_source`.

`succeeded` stays, unchanged and still written, so existing readers and the
existing service codes page keep working with no migration of behaviour.
`outcome` is not constrained to an enum, for the reason the init migration
already gives for `notification_preferences.kind` at `:247-249`: the Go side
is the authority, and a downgrade must leave rows inert rather than block a
migration.

`certificate_serial` (`...init.up.sql:236`) is `NOT NULL` with no default and
is currently always pre-allocated before the row is written
(`enrollment.go:139-153`). A `refused_source` row stores zero. That needs
saying in `model.EnrollmentRetrieval`'s comment and in the service codes UI,
which must not render a serial link for a row that has no certificate.

`model.Enrollment` and `model.EnrollmentRetrieval` gain the matching fields.

## Approval UI

On the approval screen for a service-type request, two sections, only where
the corresponding `mode` is not `disabled`.

Each is a list of candidate rows. Each row carries the address, its
provenance, a checkbox, and a widening control. The control is a stepper or
slider over prefix lengths, clamped between the candidate's own length and the
configured ceiling for its family, showing the resulting CIDR and the address
count it covers. A row selected and left alone is a `/32` or `/128`.

- **Provenance is shown on every chip.** "Seen by this server" for the entry
  matching `detail.source_ip`, "reported by the client" for the rest. The
  approver is making a different decision about each, and the existing screen
  already keeps these visually separate at `ApprovalView.svelte:163` and
  `:173`.
- **The pin section defaults to the claimed addresses; the gate section
  defaults to the observed one.** Both offer everything; the default is where
  the guidance lives.
- **Overlapping selections show collapsed**, matching what the server will
  store.
- **"Nothing selected" is visually distinct from "everything selected".** On
  the pin axis these are the same outcome and must not look the same, because
  one is a decision and the other is a wide-open pin that only looks like one.
- **`required` mode blocks the approve button** with the reason named, rather
  than failing server-side after a click.

The granted pin then flows through the existing requested-versus-granted diff
(`frontend/src/lib/approval.ts:80-86`), which currently marks `source-address`
as always trimmed because the server always dropped it. That becomes a real
comparison. The gate is not a certificate option and does not belong in that
diff; it gets its own row.

`frontend/DESIGN.md` governs the component work.

## Coordination with the anomaly policy

Both designs modify `EnrollmentService.Retrieve`, add columns touching
enrollment redemption, and add notification kinds. Neither depends on the
other to be correct, but they interact in three places.

### Refused attempts count toward the anomaly threshold

**Decided: they count.**

The anomaly detector counts *distinct source addresses, after masking*, within
a sliding window, and its own validation forbids an alert threshold below 2
because one address is every healthy job. That model is what makes counting
refusals safe:

- A **legitimate host migration** produces refusals from exactly one new
  address. The old address stops redeeming and ages out of the window. The
  distinct count is 1, which cannot cross a threshold that is validated to be
  at least 2. The owner learns about it from the gate's own refusal
  notification, which is the correct channel for "your job moved and its
  allowlist did not".
- A **stolen code and key used from many addresses** is refused by the gate
  and counted by the detector, and crosses. This is the case the detector
  exists for, and refusals are the highest-signal evidence available to it: a
  redemption that presented a valid code from a place the approver explicitly
  did not authorize. Excluding them would blind the detector precisely when it
  has the best possible input.

There is a useful ordering property here. The gate's refusal notification
fires on the *first* refusal, while the anomaly alert needs several distinct
addresses. The owner is therefore told something is wrong well before any
lock, which makes a lock the end of a visible sequence rather than a surprise.

**The residual risk**, stated plainly: a multi-address egress pool that the
approver's allowlist does not cover. A job behind a rotating NAT pool, or a
fleet migrating across several addresses at once, produces refusals from
several distinct addresses in one window, and the gate and the detector then
compound: refused *and* escalated toward a lock. The anomaly design's existing
levers are the mitigation (`ipv4_prefix` widening to collapse a pool to one
unit, or `exempt_networks` to drop it from the count entirely), and the real
fix is an allowlist that matches the egress reality, in which case no refusal
occurs at all. **[judgement]**

### The rejection note that needs amending

`service-retrieval-anomaly-policy.md`, under "Deliberately rejected", rejects
"blocking the first redemption from a new address", on the grounds that a
per-code allowlist would make every legitimate host migration a support
ticket, where a threshold model degrades gracefully.

That rejection was aimed at a **learned** allowlist, built silently from first
use. What this document proposes is an **approved** allowlist: a human chose
it, at approval time, from candidates shown to them, and the axis defaults to
`disabled` so nothing acquires a gate without somebody deciding it should. The
support burden the note describes is real and unchanged, but it is now a
strictness an operator selected deliberately for a specific enrollment, not
one the system inferred on their behalf.

That note should be amended to say so when the anomaly work is next touched.
**This document does not edit it**, to avoid mutating a settled design from
outside it; whoever implements either feature should reconcile the two in one
pass.

### Ordering

**Deferred.** Either can ship first.

- **This first:** needs nothing from the anomaly design as long as the refusal
  notification goes to the enrollment owner only, which rides the existing
  user-addressed notification path unchanged. Step 6 of the redemption
  sequence above is simply absent, and the `outcome` column and its rows are
  waiting when the detector arrives.
- **Anomaly first:** this then adds the gate check ahead of the detector's
  window evaluation, and the detector's counting query needs no change,
  because refused rows are rows in the same table with the same `source_ip`.
- **Either way**, the index the detector needs
  (`enrollment_retrievals(enrollment_id, retrieved_at)`) belongs to whichever
  ships first. Note the existing index covers `enrollment_id` alone
  (`...init.up.sql:240`), and a separate serial index already landed
  (`20260824000000_retrieval_serial_index`).

A refusal notification copied to a security mailbox rather than the owner
requires the anomaly design's one extension to `server/notify` (an `Event`
naming a literal address instead of a `users.id`). That is the only hard
dependency, and it is avoidable by keeping v1 owner-only.

## Coordination with the lifetime policy engine

`pin_source_address` on `SourcePolicyEntry`
(`server/config/types_certificates.go:243-250`) is the existing pinning
mechanism, and this proposal supersedes it. The lifetime document's own
"Known defect" section, dated 2026-08-23, already reopened it and lists five
questions; this document answers them:

1. **A separate config surface.** `cert_options.service.source_address`, its
   own block, matched on its own, with no lifetime concern in it.
2. **What gets granted.** The approver's selection, anchored to a candidate
   the request carried, bounded by an expansion ceiling and an optional
   positional allowlist. Not the observed address alone, and not the rule's
   CIDR.
3. **Enrollment-time or retrieve-time.** Enrollment time, for both axes,
   consistent with the evaluate-at-enrollment-time contract. The gate is
   *checked* at retrieval, but the networks it checks against are frozen at
   approval.
4. **Whether user certificates get a pin.** No, unchanged
   (`docs/decisions.md:87-92`).
5. **Startup validation of a `/0` pin.** Unrepresentable rather than warned
   about: `max_expansion` is validated into 8..32 and 32..128, so no selection
   can reach a default route.

**The two mechanisms must not both be live.** `approveServiceEnrollment` calls
`narrowRequestedOptionsWithPolicy` at `certrequest.go:727`, *after*
`narrowRequestedOptions` has run, and that call overwrites
`narrowed.SourceAddresses` wholesale at `lifetimepolicy.go:306`. An
approver's selection would be silently replaced by the observed address. Hence
the startup error above, rather than a precedence rule: two mechanisms
fighting over one field, with the loser's value discarded without a trace, is
not a thing to resolve at runtime.

Retiring `pin_source_address` (removing the field, its parse, its branch at
`lifetimepolicy.go:305-307`, and its tests) is step 5 of the sequencing below.
It is deliberately last, so that the replacement is proven before the
incumbent is removed, and it is deliberately a separate step, so the removal
is reviewable on its own.

Everything else in the lifetime engine, including duration tiers and the
`extensions` narrowing on `SourcePolicyEntry`, is untouched by this proposal.

## Decisions, and the reasoning behind each

### Two mechanisms rather than one generalized one

They share a config shape and a validation rule and nothing else. Their
enforcement points, their trustworthy candidate sets, their failure modes, and
their storage all differ. A single "source restriction" abstraction covering
both would have to be parameterized on every one of those, and the parameter
would be the distinction it was trying to hide.

### The gate is stored outside `option_set`

Everything in `option_set` reaches the signer and becomes a certificate
option, via `enrollment.go:174` and `sign.go:104`. The gate exists precisely
to restrict without appearing in the certificate. A shared field with a flag
saying "not this one" would be one refactor away from leaking an operator's
internal allowlist into every certificate.

### Required mode governs approval, never redemption

`mode: required` makes the approve call fail when nothing is selected for that
axis. It has no effect at redemption.

The alternative, treating an empty `retrieval_networks` as "deny everything"
when the mode is `required`, would break every enrollment approved before the
feature existed the moment an operator turned it on, with no warning and no
human present. An empty stored value means no gate, always, whatever the
current configuration says. Configuration governs what may be approved from
now on; it never retroactively reinterprets a decision already made.

### `pin.mode: required` with no claimed candidates refuses the approval

If the request carried no client-reported addresses (an older client, or any
`service enroll` before this ships), the only candidate is the observed
address, which under NAT is the NAT gateway. Forcing an approver to pin to it
produces a certificate pinned to a network shared with every other host behind
that gateway, which is the appearance of a restriction and very little of the
substance.

Better to refuse the approval, naming the reason, than to offer one wrong
choice and call it required. The operator upgrades the client, or the
administrator sets the mode to `optional`. **[judgement]**

### The refusal answers `NotFound`

Covered above under
[A refused redemption answers `NotFound`](#a-refused-redemption-answers-notfound).
It follows the precedent expiry already sets at `enrollment.go:104-108`, and
the reasoning is stronger for a live code than for a dead one.

### The refused attempt is recorded before the refusal returns

A refused redemption is the highest-value row the retrieval log can hold. It
means somebody presented a valid code from a place the approver did not
authorize, which is either a job that moved or a credential that walked.
Refusing without recording would discard exactly the evidence the feature
exists to produce, and would leave the anomaly detector nothing to count.

### Notification is coalesced, and has its own kind

A cron job redeeming every minute from a refused address is 1440 mails a day,
which trains its recipient to filter the alert. Coalescing is not a polish
item, it is what makes the notification survivable: the first refusal for a
given (enrollment, source network) sends immediately, subsequent ones inside
`notify_cooldown` are suppressed and roll into a count carried by the next
message after the window ("47 further attempts since 14:02").

It gets its own `notify.Kind` with its own preference row rather than a flag
on `service_enrollment_redeemed`, for the reason the anomaly design already
gives for the same choice: the redeemed kind is chatty and default-on, and an
operator who silenced the noise would have silenced the security signal along
with it.

The payload names the enrollment, the service account, the key ID, the public
key fingerprint, the refused address, the configured networks, and the attempt
count. **The code itself is never in the payload**, per the rule
`email-notifications.md` states and `ServiceEnrollmentCreated` already honours
(`server/notify/payloads.go:9-16`).

Delivery is queued the way every other notification is
(`server/service/notification.go:76`): it never blocks the redemption path and
never fails it. The gate refuses whether or not mail works.

### The client does not self-check its certificate

Considered and rejected. `service retrieve` could compare the pin it just
received against the host's own interfaces and warn when nothing matches,
turning the pin's opaque failure into an immediate message.

Rejected as cycles spent for no security benefit: the check is advisory, it
runs on the same host that already reported those addresses, and an attacker
holding the code simply ignores a client-side warning. The pin therefore keeps
its opaque failure mode, which is one more reason to point most operators at
the gate first.

## Deliberately rejected

**Deriving one axis from the other.** "Pin to whatever the gate allows" reads
like a convenience and is wrong in both directions: the gate's networks are
observed-address networks, which under NAT are exactly the wrong thing to pin
to, and a pin has to cover addresses that never appear at ssoosshd at all.

**Re-deriving either at retrieval.** More truthful about where the certificate
will be used, and it breaks the evaluate-at-enrollment-time contract and
silently relocates an unattended job into a different policy with no human
present to see why. Settled the same way the lifetime document settles it for
duration.

**Refusing a redemption whose observed address falls outside the certificate
pin.** Tempting, since such a certificate looks useless. It is not: the
address ssoosshd observes at redemption is legitimately different from the
address the target host sees at connect time whenever NAT is involved, which
is the common case for exactly the hosts this feature serves. Refusing would
break correct deployments to prevent a certificate that works fine.

**Learning the gate's networks from first use.** This is the thing the anomaly
document rejected, and rejecting it is why the gate is approver-chosen. A
learned allowlist fails closed on routine operations with nobody having
decided it should.

**Per-enrollment overrides of the ceilings.** The ceiling is the
administrator's bound on what approvers may do. An approver who can raise
their own ceiling does not have one.

**Pinning user certificates.** `docs/decisions.md:87-92`, unchanged. People
move between office, VPN, hotel, and tether; a pinned user certificate turns
every network change into a failed login for no gain that a shorter lifetime
does not already provide. The note there about users who ssh onward from
remote systems remains the one case worth revisiting, and is not revisited
here.

## Sequencing

Each step is independently reviewable, and the tree is working after each.

1. **Client reports addresses, with prefixes.**
   `api.LocalInterfaceAddresses` returns `ip/prefix`;
   `normalizeSourceAddresses` accepts both forms; `service enroll` sends the
   set. Inert on its own: the values are stored and displayed, and still
   dropped at approve. Ships alone, safely, and starts populating the audit
   record.
2. **Config surface and validator.** The `source_address` block, its startup
   validation, and the four-rule validation function with its tests. No caller
   yet.
3. **Pin axis end to end.** `ApproveRequestBody.SourceAddressPin`,
   `ApprovalSelection`, `narrowRequestedOptions` stops dropping, the value
   reaches `option_set` and comes out of the signer as `source-address`.
   Guarded by the startup error against `pin_source_address`.
4. **Gate axis end to end.** `retrieval_networks` column and migration,
   `outcome` column and migration, the check in `Retrieve`, the `NotFound`
   refusal, the recorded row.
5. **Refusal notification.** New kind, payload, templates, coalescing.
6. **Approval UI.** Both pickers, provenance, widening control, collapse,
   the `approval.ts` diff change, the service codes page showing
   `retrieval_networks` and refused rows.
7. **Retire `pin_source_address`.** Remove the field, its parse, the branch at
   `lifetimepolicy.go:305-307`, its tests, and the startup error from step 3.

Steps 1 and 2 are worth doing regardless of how far the rest goes.

## Testing

Per `.claude/rules/test-go.md` and `.claude/rules/go.md`: table-driven,
colocated, no mock frameworks, descriptive names.

**Validation** (`server/service`, the four-rule function). The table is the
specification: a candidate widened within the ceiling; widened past it; a
network containing no candidate; a network containing a candidate from the
*other* axis's set; both address families; a `/0` submission; an
`allowed_networks` hit and miss; empty `allowed_networks`; overlap collapse;
`max_entries` overflow; a malformed CIDR; an empty selection under each of the
three modes.

**Approval** (`server/service/certrequest_test.go`). The selection reaches
`option_set` for the pin and `retrieval_networks` for the gate, and does not
cross over. `required` fails an empty selection per axis. `disabled` discards
a selection submitted anyway. `pin.mode: required` with no claimed candidates
refuses.

**Redemption** (`server/service/enrollment_test.go`). An allowed address
redeems; a refused address answers `NotFoundError`; the refused attempt writes
a row with `outcome = refused_source` and serial zero; an empty
`retrieval_networks` never refuses, including when the mode is `required`; a
refused attempt publishes no signing job.

**Notification.** First refusal sends; a second inside the cooldown does not;
the message after the cooldown carries the suppressed count; the payload
contains no code. Assert the last one explicitly, not by inspection.

**Config** (`server/config`). Each startup validation rule, including the
`pin_source_address` conflict error.

**Client** (`internal/api/localaddrs_test.go`, `client/cmd`). Prefix-carrying
output; `normalizeSourceAddresses` accepting both forms; `service enroll`
populating the option set.

**Frontend** (`frontend/src/lib`, per `.claude/rules/test-ts.md`). The widening
control clamps to the ceiling; overlap collapse renders collapsed; provenance
labels follow `source_ip`; the `approval.ts` diff reports a real granted pin
rather than always-trimmed; `required` disables the approve button.

Anything that genuinely cannot be tested carries a `not covered:` comment at
the block saying why, per the repository rule. "Awkward to reach" is a test to
write, not a note to add.

## Provenance: what was verified and how

Every anchor in this document was read at `5d23809` (2026-08-24), the same
commit `service-retrieval-anomaly-policy.md` was verified against.

Verified by direct read: `client/cmd/service_enroll.go:73`;
`internal/api/localaddrs.go:33-72`; `server/service/certrequest.go:241-305`,
`:67-70`, `:587`, `:718-727`; `server/service/certtypepolicy.go:66-73`;
`server/service/lifetimepolicy.go:243-311`; `server/service/enrollment.go:94-185`;
`server/signer/sign.go:90-111`; `server/model/enrollment.go` in full;
`server/webtypes/webtypes.go:87-97`, `:205-245`;
`server/controller/certrequests.go:355-375`;
`server/controller/enrollment.go:60`;
`server/config/types_certificates.go:190-251`;
`server/resources/migrations/postgres/20260101000000_init.up.sql:198-254`;
`server/notify/notify.go:72-80`; `server/notify/payloads.go:1-18`;
`server/service/notification.go:72-80`; `server/mail/sender.go:15-22`;
`server/webtypes/webtypes.go:140`; `server/config/types_certificates.go:82-88`;
`frontend/src/lib/components/ApprovalView.svelte:160-200`;
`docs/decisions.md:87-92`; `docs/email-notifications.md:13`.

Verified by listing: `server/resources/migrations/postgres/` contains the init
pair plus `20260824000000_retrieval_serial_index`; `frontend/DESIGN.md` and
`internal/api/localaddrs_test.go` both exist.

Taken from `service-retrieval-anomaly-policy.md` rather than re-derived: the
counting model, the normalization prefixes, the state machine, the config
shape, and the `notify.Event` address extension. Re-read that document before
implementing anything in
[Coordination with the anomaly policy](#coordination-with-the-anomaly-policy).

## Open questions

1. **Ordering against the anomaly policy.** Deferred deliberately. Both
   orderings work; see [Ordering](#ordering) for what each needs.
2. **Whether the refusal notification should also reach a security mailbox.**
   Owner-only in v1 keeps this independent. A copy to a fixed address needs
   the anomaly design's `notify.Event` extension, and once that exists the
   question is whether an address refusal deserves the same escalation an
   anomaly lock gets.
3. **Whether the gate should be editable after approval.** It is server-side
   state, so unlike the pin it *can* be. A migrated host would then be an edit
   rather than a re-enrollment. That is a new authorization question (the
   approver, an admin, or both?) and a new audit surface, so it is out of
   scope here and worth its own pass.
4. **Whether `max_entries` wants a lower default on the pin axis.** Eight
   networks joined into one `source-address` option is a long critical option
   on every certificate. No limit is known to be exceeded; the number is a
   guess.
