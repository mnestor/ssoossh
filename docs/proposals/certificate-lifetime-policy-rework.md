# Certificate lifetime policy: the unfinished half

**Status:** proposal. Not scheduled.

**Anchors verified at:** `73eba7c`. `file:line` references drift; re-check
before relying on one.

The lifetime policy engine ships and is documented in
[operations/certificate-lifetime-policy.md](../operations/certificate-lifetime-policy.md).
This records what it deliberately left unbuilt, and one defect in what it did
build.

## Defect: `source-address` is welded to the lifetime rule

`pin_source_address` is a field on `SourcePolicyEntry`
(`server/config/types_certificates.go`), the same struct that carries
`max_duration`. That puts two unrelated policy questions on one rule list:

- **How long** may a certificate issued to a client on this network live?
  User and service certificates.
- **From which addresses** may the issued certificate be used? A critical
  option written into the certificate, service certificates only.

The first is a cap the server applies at issuance. The second is a restriction
the certificate carries to every host it is later presented to. They take a
CIDR as input and share nothing else, but the implementation makes them share
the rule list, the match, the tie-break, and the enable condition.

### What the sharing costs

1. **One match, two answers.** `evaluateSourceRule` and
   `narrowRequestedOptionsWithPolicy` (`server/service/lifetimepolicy.go`)
   each run their own copy of the same longest-prefix loop and take the answer
   from the single winning entry. Refining the lifetime map silently rewrites
   the pinning map: add `10.20.5.0/24` with a shorter `max_duration` inside an
   already-pinned `10.20.0.0/16`, forget to repeat `pin_source_address: true`,
   and every enrollment from that lab quietly stops being pinned. Nothing
   warns.
2. **The tie-break is a duration comparison.** On equal prefix length the
   winner is the entry with the shorter `max_duration`. Whether a certificate
   is pinned is therefore decided by which rule grants less time, a comparison
   with no bearing on address restriction.
3. **Two copies of the matcher**, kept in sync by hand, because one caller
   wants a duration and the other wants an option.
4. **Enabling either enables both.** `isLifetimePolicyConfigured` treats a
   non-empty `source_policy` as a configured lifetime policy, switching on
   tier evaluation. An operator who wants pinning without tiering gets the
   tier machinery anyway, and walks into the next item.
5. **Pin-only config yields zero-length certificates.**
   `LifetimePolicy.DefaultDuration`'s doc comment promises that a zero falls
   back to the enclosing `ValidDuration`. `evaluateDuration` does not
   implement that fallback. With no tier matched and no `default_duration`,
   the effective duration is zero and survives both the `min` against the
   source rule and the ceiling clamp. The enrollment is live, but every
   redemption produces a zero-length span the signer rejects
   (`server/signer/sign.go`). Config that only wanted a pin must set an
   unrelated duration to work at all. It fails closed, and it fails at
   redemption rather than approval, so the operator sees it only when the
   unattended job first runs.
6. **`extensions` has the same shape of problem**, less sharply: per-network
   extension narrowing is answered by whichever entry won the duration match.

### The granted value is not the configured one

`narrowRequestedOptionsWithPolicy` writes the single observed address
(`narrowed.SourceAddresses = []string{sourceIP}`,
`server/service/lifetimepolicy.go:307`). The signer joins that into the
critical option, so the certificate carries one bare address, which OpenSSH
reads as a `/32` or `/128`.

Both the config comment and the operations doc describe a pin to the rule's
CIDR. Neither is what happens.

- The `cidr:` an operator writes selects the rule and contributes nothing to
  what is granted. Widening it changes which hosts match; it never widens the
  pin.
- Under NAT the observed address is the NAT egress, so the pin names the
  gateway rather than the service host, and every host behind that gateway
  satisfies it equally.
- The union built for exactly this case is discarded. `CreateRequest` merges
  the client's reported interface addresses with the observed source IP and
  persists the result (`server/service/certrequest.go:302-304`); the approve
  path drops the list; the policy then rebuilds a one-element list from the
  observed IP. The stored union is audit data and nothing more.
- A `/0` entry with pinning on is not the harmless no-op the config comment
  implies. Because the pin is a `/32` of the observed address rather than the
  rule's range, it pins every certificate to whatever address its enrollment
  happened to come from. `validateStartupConfig` checks `trusted_proxies` and
  stops.

### Related: `service enroll` reports no addresses

`ssh login` and `pam_ssoossh` both populate
`RequestedOptions.SourceAddresses` from `api.LocalInterfaceAddresses()`.
`service enroll` sends an empty option set, so the only address recorded for
an enrollment is the one ssoosshd observed. It should send them too, for
parity and because a service host is precisely the NAT case that function's
doc comment was written for.

The one-line client fix is inert alone: the list is dropped at approve and the
pin is rebuilt from the observed IP regardless. It belongs with the rework.

### What the rework has to decide

Questions, not a plan.

1. **A separate config surface.** A distinct CIDR-keyed list for address
   restriction, matched on its own, so the two maps can be refined
   independently. `pin_source_address` goes away rather than gaining a
   sibling.
2. **What gets granted.** The rule's CIDR, the observed address, the
   request's reported addresses intersected with the rule's CIDR, or the union
   of the reported set and the observed address. Narrowing-only must hold for
   whichever wins: a client-supplied address may survive an intersection,
   never be granted on the client's say-so.
3. **Enrollment-time or retrieve-time.** Settled as enrollment-time for
   lifetime. Pinning has both the stronger case for retrieve-time, since it
   describes where the certificate will actually be used, and the stronger
   objection, since a relocated unattended job then silently stops working
   with no human present to see why.
4. **Whether user certificates get a pin at all.**
   [project/decisions.md](../project/decisions.md) declines it, noting it may
   be worth reconsidering for users who ssh onward from remote systems.
5. **Startup validation.** Whatever a `/0` pin means once (2) is decided
   should be a startup error or warning, not a comment.

## Not built: runtime-editable policy

Config-file rules are not editable at runtime. For a source that is, two
independent enforcement points, because one is not enough:

1. **On save**, reject a rule exceeding the configured ceiling — immediate
   feedback, and the obvious place to catch a mistake.
2. **On evaluation**, clamp to the ceiling anyway. This is the one that holds
   the invariant: the ceiling can be lowered in the config file later, and
   stored rules written under a more generous ceiling must not survive it.
   Store what the administrator entered, clamp when applying.

The effective value stays `min(config ceiling, matching rule)`, so the worst a
compromised admin session achieves is policy reverting to the config file's
own limits — a denial of service, not an escalation.

Two supporting requirements:

- The approval page must show a shortened lifetime *and its reason* ("this
  network: 1h") before anyone approves, for the same reason it already shows
  trimmed options.
- **Record the decision, not just the log line.** Which rule applied belongs
  on the approval record, not only in the access log. "Why did this
  certificate get one hour?" is asked months later by someone reading
  issuance history, not by someone with that day's logs open.

### Prerequisites

None of this is buildable today:

- **There is no admin concept.** No role on `model.User`, no admin-scoped
  endpoints. Editable policy needs an answer to "who may edit", and that
  answer is an authorization model, not a checkbox.
- **Persistence.** A policy table, plus precedence rules against the config
  file that survive a config change.
- **An audit trail.** Who changed which rule, when, and what it was before. A
  policy that can be changed at runtime and cannot be reviewed afterwards is
  worse than one that cannot be changed.

The engine has none of those prerequisites, which is why it was built first.
A UI becomes a second source of rules for the same engine rather than a
parallel implementation of it.

## Rejected: scoring

An earlier draft scored an address across `trusted_subnets`,
`untrusted_subnets`, and `reverse_dns` patterns, then mapped the total onto a
cap. Direct per-subnet rules replaced it:

- **"Why did this certificate get 15 minutes?"** must be answerable by
  pointing at one rule. Under scoring it is arithmetic over several partial
  matches.
- **Scores cannot be edited safely in a UI.** Changing one weight silently
  moves every network across every threshold, the opposite of a change an
  administrator can review before saving.

Reverse-DNS scoring went with it: a PTR lookup on the approval path buys a
weak signal for a network round trip, a timeout policy, and a cache.

## Open question

**Does a shortened lifetime need its own approval-page treatment?** The page
shows trimmed *options* struck through; a shortened lifetime is a different
shape of narrowing and may want different wording rather than being squeezed
into the same diff list.
