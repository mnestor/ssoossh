# Certificate lifetime & approval policy

> **Note:** the "service account linkage" open item below is resolved: the
> approver chooses the account in the web UI and it becomes the certificate
> principal.

**Status: implemented.** The policy engine described here exists as
`server/service/lifetimepolicy.go` (`lifetimePolicyEngine`: group tiers,
source-address rules, most-specific-wins against a per-type ceiling) and is
what `Approve` evaluates. This document remains the design rationale — why
the precedence rules are what they are — and the reference for the
semantics the engine's tests assert. `RequireGroup` enforcement and
extension narrowing were settled separately and are unchanged by it.

## Hard constraints on the design

- **The config file is the outer bound, and only the config file can raise
  it.** Nothing reachable over HTTP may make issuance more permissive than
  the loaded configuration allows: not longer-lived, not more capable. Policy
  that only ever *reduces* what a certificate can do may be edited at runtime;
  see "Runtime-editable narrowing" below.

  **Amends an earlier, stricter constraint** (2026-08-14). This document
  previously read "config-file only, no runtime reconfiguration… treated as a
  security boundary, not a convenience trade-off to revisit later", and the
  web UI's own delivery phase document said the same about `/admin/*` before
  that document was removed from the tree. That rule is relaxed deliberately,
  not forgotten, and the
  security property it existed to protect is kept: a caller who compromises
  the web tier still cannot obtain a certificate the config file would not
  already have allowed. What they gain is the ability to make issuance
  *stricter*, which is a denial of service, not an escalation.

  The general form is the one this product already runs on — client requests
  ask, the web UI narrows, server config gates (root CLAUDE.md, Hard
  Constraints). This extends that from per-request options to standing
  policy, with the administrator in the "narrows" position.
- **Config must support both simple and tiered setups without forcing
  either.** An admin who doesn't care about groups should be able to say
  "everyone gets 10 hours" with no group list at all. An admin who wants
  per-group tiers should be able to add them without restructuring
  anything. Concretely: a flat default plus an optional, ordered list of
  group-based overrides.

## Source-network policy

**Decided.** Certificates are restricted according to the network the
requesting client came from: a laptop on the office range or the VPN can hold
a workday-long certificate, the same laptop on hotel wifi gets minutes.

### What "narrowing only" means precisely

A rule may only reduce what a certificate can do:

- **Shorter lifetime.** Never longer.
- **Fewer extensions.** Intersection with the type's configured set.
- **More restricting critical options**, on the service path only. Notably
  `source-address`, pinning the certificate to the range it was issued for, so
  a stolen one is useless elsewhere. Adding a critical option reads like
  widening and is the opposite: it can only prevent uses that would otherwise
  be allowed.

  **Not for user certificates.** A person moves — office, VPN, hotel, phone
  tether — and pinning their certificate to an address turns every change of
  network into a failed login for no security gain that shortening the
  lifetime does not already provide. Users have other levers; services, which
  sit still, do not. (`force-command` stays out of subnet policy entirely —
  "which command" is not a property of a network.)

`source-address` is worth calling out because the option is currently dropped
unconditionally at approve, for the stated reason that no server config exists
to bound it against. This is that bound.

### Which certificate types

**User and service only.** PAM certificates keep their fixed lifetime,
matching the scope table below: they are already the shortest thing the
server issues, and a PAM authentication is a local event on a machine the
user is sitting at, so the address adds no signal worth a second mechanism.

Service certificates *are* in scope, but note the interaction with phase 8:
they are signed at `retrieve`, potentially months after approval and possibly
from a different address than the enrollment came from. Decide explicitly
whether the rule is evaluated against the enrolling address (frozen with
everything else at approval) or the retrieving one — the latter is more
truthful about where the certificate will be used, but it means an unattended
job can silently drop into a stricter tier by being moved, with no human
present to see why issuance changed.

### Which address

The address recorded on the request (`certificate_requests.source_ip`, from
`g.ClientIP()` at create time) — where the certificate will be *used*. Not the
approver's browser address, which is a different machine on a different
network and is nothing to do with it.

That value is trustworthy: `SetTrustedProxies` is always called
(`server/bootstrap/router.go`), so `X-Forwarded-For` is ignored unless an
operator explicitly trusts a proxy range. `RequestedOptions.SourceAddresses`
is client-supplied and unverified, and must never feed policy.

**The footgun this creates:** an operator behind a reverse proxy who has not
set `http.trusted_proxies` sees every client as the proxy's address — very
likely an internal range, so *everyone* silently lands in the most generous
tier. That is the failure mode where this feature quietly does the opposite of
its purpose. Warn at startup when source-network policy is configured and
`trusted_proxies` is empty, and log a warning when a request carries
`X-Forwarded-For` from an untrusted peer.

### Matching

**Longest-prefix wins**, as in a routing table — order-independent, which
matters when rows are being added and reordered in a UI. Ties (equal prefix
length) resolve to the stricter rule, so a duplicate can never accidentally
loosen anything.

**No match means no reduction**: the type's configured ceiling applies. Rules
*are* reductions, so the absence of one is coherently "nothing to reduce". An
administrator who wants a default floor writes an explicit `0.0.0.0/0` and
`::/0` pair, the same idiom as a firewall's default rule. Documented loudly,
since the alternative reading (unmatched networks get the strictest tier)
would be a reasonable guess and is not what happens.

IPv4 and IPv6 rules both work and never match each other. Addresses are
normalized with `netip.Addr.Unmap()` first, so an IPv4-mapped IPv6 form
(`::ffff:10.0.0.1`) matches a `10.0.0.0/8` rule rather than silently missing
every rule.

### Who owns a rule

**Global subnet rules** live in the config file, as drafted above. They are the
deployment-wide statement — "anything off the VPN gets fifteen minutes" — and
only someone with filesystem access changes them. Today that is the only
source, which makes "who may edit this" the same question as "who has
filesystem access on the server".

A second, runtime-editable source is the obvious next step, and the design
below is written for it — but it needs an owner concept first, which does not
exist yet. See "Prerequisites".

When a second source does land, the rule to state up front is that a more
specific rule **wins outright**: not merged with, averaged against, or bounded
by the config rule that also matches. Both directions only ever narrow
relative to the type ceiling, so "most specific wins" and "strictest wins"
agree today — but saying which source wins, rather than that the minimum
wins, is what keeps it predictable once there is more than one.

### Runtime-editable narrowing

Config-file rules are not editable at runtime at all. For a source that is,
two independent enforcement points, because one is not enough:

1. **On save**, reject a rule that exceeds the configured ceiling — immediate
   feedback, and the obvious place to catch a mistake.
2. **On evaluation**, clamp to the ceiling anyway. This is the one that
   actually holds the invariant: the ceiling can be *lowered* in the config
   file later, and stored rules written under a more generous ceiling must not
   survive it. Store what the administrator entered, clamp when applying.

So the effective value is always `min(config ceiling, matching rule)`, and the
worst a compromised admin session achieves is policy reverting to the config
file's own limits.

The approval page must show a shortened lifetime *and its reason* ("this
network: 1h") before anyone approves, for the same reason it already shows
trimmed options: a human authorizing issuance has to see what they are
authorizing.

**Record the decision, not just the log line.** Which rule applied — and, if
scoring is ever reintroduced, how the number was reached — belongs on or
linked to the approval record, not only in the access log. "Why did this
certificate get one hour?" is asked months later by someone reading the
issuance history, not by someone with the logs from that day open.

### Prerequisites

None of this is buildable today, and the missing pieces are not small:

- **There is no admin concept.** No role on `model.User`, no admin-scoped
  endpoints; phase 3 recorded an admin-wide view as "a later decision" and
  phase 4 left `/admin/*` unbuilt for exactly that reason. Editable policy
  needs an answer to "who may edit" first, and that answer is an
  authorization model, not a checkbox.
- **Persistence.** A policy table, plus precedence rules against the config
  file that survive a config change.
- **An audit trail.** Who changed which rule, when, and what it was before.
  A policy that can be changed at runtime and cannot be reviewed afterwards
  is worse than one that cannot be changed.

The policy *engine* — evaluation, matching, clamping — has none of those
prerequisites and can be built first, reading rules from the config file. The
UI then becomes a second source of rules for the same engine rather than a
parallel implementation of it.

### This supersedes the scoring draft below

The earlier draft scored an address across `trusted_subnets`,
`untrusted_subnets`, and `reverse_dns` patterns, then mapped the total onto a
cap. Direct per-subnet rules replace that, for two reasons:

- **"Why did this certificate get 15 minutes?"** must be answerable by
  pointing at one rule. Under scoring it is arithmetic over several partial
  matches, and the doc's own step 5 had to add logging of every contributing
  signal just to make it explainable.
- **Scores cannot be edited safely in a UI.** Changing one weight silently
  moves every network across every threshold, which is the opposite of a
  change an administrator can review before saving. Direct caps make each row
  mean exactly what it says.

Reverse-DNS scoring is dropped from the first cut with it: a PTR lookup on the
approval path buys a weak signal for a network round trip, a timeout policy,
and a cache — see open question 2, which exists only because of it. The
group-based `tiers` half of the draft is unaffected and still stands; the two
compose as `min(tier, subnet rule, ceiling)`.

## Scope per certificate type

| Type | Approval gate | Duration |
| --- | --- | --- |
| **User** | (existing `RequireGroup`, if any) | Full tiered policy (see below) |
| **Service** | Optional group membership **and** account linkage — see below | Tiered, same mechanism as User |
| **PAM** | N/A (out of scope — see `docs/signing-pipeline.md`'s deferral of service and pam until User is fully working) | Always fixed/time-locked, no tiering at all |

PAM needs no new mechanism — its fixed lifetime is a constant, not policy.
The new tiered mechanism below is for User (fully) and Service (reusing the
same tier evaluation, with an additional account-linkage gate layered on
top).

### Service account linkage (open item, needs a schema decision)

You described Service approval as needing to check that the *approving*
identity is actually associated with the service account being requested —
not just "is in the required group." `service.Identity` already has a
`ServiceAccounts []string` field (populated from OIDC claims), which is
the natural place to check against — but the create-request flow currently
has no field identifying *which* service account a `service enroll` call
is for. That's a real schema gap, not just a policy-evaluation gap:

- `apitypes.ServiceEnrollRequestBody` needs a new field (e.g.
  `ServiceAccount string`) identifying the account being requested.
- `Approve`'s service branch needs a check: `requireGroup` (if set) AND
  `slices.Contains(identity.ServiceAccounts, req.ServiceAccount)` (if
  linkage is enabled — see "semi-changeable" below, this gate might itself
  want to be optional/configurable, matching "gated behind *optional*
  group membership").

This needs its own confirmation pass before implementing — it's a new
request-body field and a new DB column (`certificate_requests` doesn't
currently store "which service account"), not just a policy tweak.

## Config schema (draft)

```yaml
cert_options:
  user:
    lifetime_policy:
      default_duration: 10h        # used when no tier matches, or no tiers configured at all
      tiers:                       # optional — omit entirely for flat "everyone gets default_duration"
        - group: contractors
          max_duration: 1h
        - group: engineers
          max_duration: 10h
      # Narrowing only, and editable in the web UI within the ceiling above.
      # Longest prefix wins; no match means no reduction.
      source_policy:
        - cidr: 10.0.0.0/8         # office and VPN: no reduction
          max_duration: 10h
        - cidr: 192.168.0.0/16     # lab
          max_duration: 4h
        - cidr: 0.0.0.0/0          # everywhere else: short, and pinned
          max_duration: 15m
          extensions: [permit-pty] # intersected with the type's set
          pin_source_address: true # add the source-address critical option
        - cidr: "::/0"             # v4 rules never match v6 addresses
          max_duration: 15m
  service:
    require_group: ""              # already exists
    require_account_linkage: true  # new — see "Service account linkage" above
    lifetime_policy: {}            # same shape as user's, reused as-is
```

Notes on this draft:

- `lifetime_policy` is a new nested block so it can be added under both
  `user` and `service` without duplicating field names at the top level of
  each `CertOptions*` struct.
- Every list (`tiers`, `trusted_subnets`, `untrusted_subnets`,
  `reverse_dns`, `score_caps`) is independently optional — a config that
  sets none of them reduces to exactly "everyone gets `default_duration`,"
  satisfying the "admin doesn't care about groups" case with zero extra
  YAML.
- `source_policy` entries are caps, not replacements: the effective duration
  is `min(tier_duration, subnet_rule, valid_duration)`, consistent with
  "intersection is the shortest lifetime" already established in
  `docs/dev/ssoossh-context.md`. `valid_duration` is in that expression on
  purpose — it is the ceiling a UI-supplied rule is clamped to, and it is
  re-applied at evaluation rather than trusted from write time.

## Algorithm (draft)

1. **Tier duration**: walk `tiers` in order, first `identity.Groups` match
   wins; fall back to `default_duration` if none match (or if `tiers` is
   empty).
2. **Source-address score**: start at 0.
   - Add the score of the first matching entry in `trusted_subnets` (if
     any), checked against the address(es) supplied in
     `RequestedOptions.SourceAddresses` — remember these are
     client-self-reported and unverified, so a match here can only ever
     raise confidence, consistent with "may shorten a lifetime when
     absent but never extend one when present" from `docs/ssoossh-
     context.md`. Union with the server-observed connecting IP the same
     way source-address narrowing already conceptually does.
   - Add the score of the first matching entry in `untrusted_subnets`.
   - Add the score of the first matching `reverse_dns` wildcard pattern
     against the observed IP's PTR record (a real DNS lookup at approval
     time — needs a timeout and a sane default deny/neutral-score
     behavior if the lookup fails or times out).
3. **Score cap**: walk `score_caps` in descending `min_score` order, first
   entry where the computed score `>= min_score` gives the cap duration.
4. **Final duration** = `min(tier_duration, score_cap_duration)`.
5. **Log the winning signal at each step** (which tier matched, which
   subnet/pattern contributed to the score, which cap applied) — not just
   the final number, so "why did this cert get 15 minutes" is answerable
   directly from the log line, not by re-deriving the computation.

## Open questions to resolve before implementing

1. ~~**Service account linkage schema**~~ — answered: the
   service account is chosen by the approver in the web UI from their
   entitled set, not named by the client, so there is no new request-body
   field. The approve handler validates the choice against
   `identity.ServiceAccounts`. A column on the request (or the enrollment) (feat/service-certs has added certificate_requests.service_account). STEP 1 note: This policy engine computes duration at approval time; the service enrollment contract is evaluate-at-enrollment-time, never re-derive-at-retrieve-time.
   is still needed to record which account was chosen.
2. ~~**Reverse-DNS lookup mechanics**~~ — moot. Reverse-DNS scoring is
   dropped; see "This supersedes the scoring draft below".
3. ~~**Multiple trusted/untrusted matches**~~ — answered: longest prefix
   wins, ties resolve to the stricter rule. Order-independent, which
   first-match-wins is not, and that matters once rows are editable.
4. **`RequestedOptions.SourceAddresses` trust boundary** — answered: it does
   not feed policy at all. It is client-submitted and unverified, and the
   observed `source_ip` is trustworthy on its own (see "Which address"), so
   there is no reason to mix an attacker-controlled value into the decision.
   It remains a *request* for a `source-address` critical option, bounded
   like any other request.
5. **Where does this live in `server/service`?** — `resolveCertOptions`
   currently returns `(narrowed, validDuration, requireGroup, err)`
   pulling a flat `ValidDuration` straight off config. This plan replaces
   that duration computation with the multi-step algorithm above, scoped
   to `CertificateTypeUser`/`CertificateTypeService` only (PAM keeps its
   existing flat lookup unchanged). Likely a new `lifetimePolicy` type/file
   alongside `keyid.go`, not a `resolveCertOptions` in-place expansion,
   given the added complexity.

6. **Does a shortened lifetime need its own approval-page treatment?** The
   page shows trimmed *options* struck through; a shortened lifetime is a
   different shape of narrowing and may want different wording rather than
   being squeezed into the same diff list.

## Suggested order

1. **The policy engine, config-file only.** Evaluation, longest-prefix
   matching, clamping to the ceiling, and the startup warning about
   `trusted_proxies`. No new authorization model, no persistence, no UI —
   buildable today, and it is the piece that actually enforces anything.
2. **The approval page showing why a lifetime was shortened.** Small, and it
   closes the "a human sees what they authorize" loop for this feature.
3. **An admin authorization model.** The real prerequisite for everything
   below, and useful well beyond this feature.
4. **Persistence and the audit trail**, then the editing UI on top, feeding
   the same engine from step 1.

Steps 1 and 2 are worth doing regardless of whether the UI is ever built.

## STEP 1 Complete: Config-File-Only Policy Engine

**Status:** Implemented and verified.

### Settled decisions:

- **SourceAddresses:** UN-DROPPED for service certificates only. Narrowed through `pinSourceAddress` config field in source policy rules. User certificates do not support source-address pinning (per plan line 62-65: "Not for user certificates"). This is the narrowing-only invariant: the policy can only restrict what addresses are allowed (to just the source IP), never expand them.

  **Reopened 2026-08-23.** Attaching this to `SourcePolicyEntry` conflates
  address restriction with lifetime capping, and what is granted is the
  observed address rather than the configured network. See "Known defect:
  `source-address` is welded to the lifetime rule" below before building on
  this.

- **ForceCommand:** STAYS DROPPED (fail-closed). The plan explicitly states line 64: "stays out of subnet policy entirely — 'which command' is not a property of a network.'" No narrowing mechanism for it. This is an intentional design decision, not missing functionality. Waiting for this feature to exist is complete.

- **Certificate on the wire in multi-instance wake message:** Accepted, fine. Already published as public data. (Marked resolved in docs/dev/multi-instance-safety-plan.md.)

- **Service-account linkage:** Evaluated at enrollment time, never re-derived at retrieve time. feat/service-certs has added `certificate_requests.service_account` schema column.

## Known defect: `source-address` is welded to the lifetime rule

**Status:** identified 2026-08-23. Not scheduled. This needs rework, not a
patch, and the settled decision recorded in STEP 1 above is the thing being
reopened.

`pin_source_address` is a field on `SourcePolicyEntry`
(`server/config/types_certificates.go:172-195`), the same struct that exists
to carry `max_duration`. That puts two unrelated policy questions on one rule
list:

- **How long** may a certificate issued to a client on this network live?
  Applies to user and service certificates.
- **From which addresses** may the issued certificate be used? A critical
  option written into the certificate, service certificates only.

The first is a cap the server applies at issuance. The second is a
restriction the certificate carries to every host it is later presented to.
They take a CIDR as input and share nothing else, but the implementation
currently makes them share the rule list, the match, the tie-break, and the
enable condition too.

### What the sharing costs

1. **One match, two answers.** `evaluateSourceRule`
   (`server/service/lifetimepolicy.go:198-241`) and
   `narrowRequestedOptionsWithPolicy` (`:270-295`) each run their own copy of
   the same longest-prefix loop, and each takes its answer from the single
   winning entry. Refining the lifetime map therefore silently rewrites the
   pinning map. Add `10.20.5.0/24` with a shorter `max_duration` for a lab
   inside an already-pinned `10.20.0.0/16`, forget to repeat
   `pin_source_address: true`, and every enrollment from that lab quietly
   stops being pinned. Nothing warns.
2. **The tie-break is a duration comparison.** When two entries have equal
   prefix length the winner is the one with the shorter `max_duration`
   (`:230`, `:288`). Whether a certificate is pinned can therefore be decided
   by which of two rules happens to grant less time, a comparison with no
   bearing on address restriction.
3. **Two copies of the matcher, kept in sync by hand.** Same loop, same
   tie-break, written twice, because one caller wants a duration out of it and
   the other wants an option.
4. **Enabling either enables both.** `isLifetimePolicyConfigured` (`:15-17`)
   treats a non-empty `source_policy` as a configured lifetime policy, which
   switches on tier evaluation. An operator who wants pinning and no lifetime
   tiering gets the tier machinery anyway, and walks into the next item.
5. **Pin-only config yields zero-length certificates.**
   `LifetimePolicy.DefaultDuration`'s doc comment promises "If zero, the
   enclosing `CertOptions*.ValidDuration` is used instead"
   (`server/config/types_certificates.go:139-141`). `evaluateDuration` does
   not implement that fallback (`server/service/lifetimepolicy.go:139-169`).
   With no tier matched and no `default_duration`, the effective duration is
   zero, and it survives both the `min` against the source rule and the
   ceiling clamp untouched. `approveServiceEnrollment` records that zero on
   the enrollment as `certificate_duration_seconds`; the enrollment itself is
   live (its own expiry comes from `cert_options.service.enrollment_duration`,
   not from this), but every redemption produces a zero-length span that the
   signer rejects outright (`server/signer/sign.go:134`). Config that only
   ever wanted a pin has to set an unrelated duration to work at all. It fails
   closed, and it fails at redemption rather than at approval, so the operator
   sees it only when the unattended job first runs.
6. **`extensions` has the same shape of problem**, less sharply. Per-network
   extension narrowing is a real question, but it too is answered by whichever
   entry won the duration match.

### The granted value is not the configured one

Separate from the coupling, and the same rework should settle it.

`narrowRequestedOptionsWithPolicy` writes the single observed address:
`narrowed.SourceAddresses = []string{sourceIP}` (`:307`). The signer joins
that list into the critical option (`server/signer/sign.go:100-106`), so the
certificate carries one bare address, which OpenSSH reads as a `/32` or
`/128`.

Both the config comment ("pinning the certificate to this network",
`server/config/types_certificates.go:187-194`) and this document's own text
("pinning the certificate to the range it was issued for", under "What
'narrowing only' means precisely") describe a pin to the rule's CIDR. Neither
is what happens.

- The `cidr:` an operator writes selects the rule and contributes nothing to
  what is granted. Widening it changes which hosts match; it never widens the
  pin.
- Under NAT the observed address is the NAT egress, so the pin names the
  gateway rather than the service host, and every host behind that gateway
  satisfies it equally.
- The union built for exactly this case is discarded before it can help.
  `CreateRequest` merges the client's reported interface addresses with the
  observed source IP and persists the result
  (`server/service/certrequest.go:240-266`); `narrowRequestedOptions` drops
  the whole list at approve (`server/service/certtypepolicy.go:59-64`); the
  policy then rebuilds a one-element list from the observed IP. The stored
  union is audit data and nothing more.
- The "a `/0` with `PinSourceAddress=true` is a warning sign" note in the
  config comment is enforced nowhere. `validateStartupConfig`
  (`server/service/lifetimepolicy.go:335-352`) checks `trusted_proxies` and
  stops. Worse, because the pin is a `/32` of the observed address rather
  than the rule's range, a `0.0.0.0/0` entry with pinning on is not the
  harmless no-op the comment implies: it pins every certificate to whatever
  address its enrollment happened to come from.

### Related: `service enroll` reports no addresses

`ssh login` (`client/cmd/ssh_login.go:265-268`) and pam_ssoossh
(`pam_ssoossh/auth.go:69`) both populate `RequestedOptions.SourceAddresses`
from `api.LocalInterfaceAddresses()`. `service enroll` sends an empty option
set (`client/cmd/service_enroll.go:73`), so the only address ever recorded
for an enrollment is the one ssoosshd observed. It should send them too, for
parity and because a service host is precisely the case the NAT reasoning in
`LocalInterfaceAddresses`' doc comment was written for.

The one-line client fix is inert on its own: the list is dropped at approve
today and the pin is rebuilt from the observed IP regardless. It belongs with
the rework, so it lands alongside a decision about which addresses may
actually be granted.

### What the rework has to decide

Questions, not a plan.

1. **A separate config surface.** A distinct CIDR-keyed list for address
   restriction, matched on its own, so the lifetime map and the pinning map
   can be refined independently. `pin_source_address` on `SourcePolicyEntry`
   goes away rather than gaining a sibling.
2. **What gets granted.** The rule's CIDR, the observed address, the
   request's reported addresses intersected with the rule's CIDR, or the
   union of the reported set and the observed address. Narrowing-only has to
   hold for whichever wins: a client-supplied address may survive an
   intersection, never be granted on the client's say-so.
3. **Enrollment-time or retrieve-time.** Settled as enrollment-time for
   lifetime (see "Which certificate types"). Pinning has both the stronger
   case for retrieve-time, since it describes where the certificate will
   actually be used, and the stronger objection, since a relocated unattended
   job then silently stops working with no human present to see why.
4. **Whether user certificates get a pin at all.** `docs/decisions.md`
   declines it, with a note that it may be worth reconsidering for users who
   ssh onward from remote systems.
5. **Startup validation.** Whatever a `/0` pin means once (2) is decided
   should be a startup error or warning, not a comment.

## To resume this

Tell Claude to read this file and turn it into an implementation plan (or
straight into code). The source-network half is decided and ordered above;
the group-tier half still needs its own pass.
