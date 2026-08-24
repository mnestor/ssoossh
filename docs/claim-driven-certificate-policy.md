# Claim-driven certificate policy

**Status: designed, nothing built.** No code has been written for this. The
design below is settled to the point where an implementation plan can be
derived from it, but it has not been re-checked against the tree since
`023c0a8` (2026-08-24).

> **Before planning from this document, do two things.**
>
> 1. **Re-run the verification pass** in [Provenance](#provenance-what-was-verified-and-how).
>    Every `file:line` anchor here is as of `023c0a8` and will drift. Three
>    of the findings are defects that may have been fixed independently; if
>    so, the sequencing changes.
> 2. **Re-read the reasoning, not just the decisions.** Every decision in
>    [Decisions](#decisions-and-the-reasoning-behind-each) carries the
>    argument that produced it, because the argument is the part that ages.
>    Several were made on judgement calls about operator behaviour and
>    deployment shape, not on facts about the code. Those are the ones worth
>    re-opening first, and they are flagged.

This extends [certificate-lifetime-policy.md](certificate-lifetime-policy.md),
which is implemented. Read that first: it establishes the engine, the
precedence rules, and the narrowing-only invariant that this design works
within.

## What this proposes

An operator configures an extra OIDC claim carrying a number, for example a
Level of Confidence expressing how far a person has been vetted. That number
should be able to shape certificate issuance: shorter lifetimes for lower
scores, fewer extensions, and, at the extreme, no certificate of that type at
all.

The requirement that shapes the whole design is that **nothing about that
claim may be hardcoded**. The server must not know what a Level of Confidence
is, must not name it, and must not assume any deployment has one. It knows
only that a claim exists under an operator-chosen name and can be compared
numerically.

The mechanism is a small extension of what the lifetime policy engine already
does. Today a tier matches on group membership. It should match on a
condition, of which "is in this group" is one case.

## Why the config already gestures at it

`ssoosshd.yaml` carries a commented-out block predating this work:

```yaml
#   user:
#     valid_duration: 300s
#     # require_auth_level: 30
#     # levelhours:
#     #   # highest authlevel without going over wins
#     #   - "30:1" # authlevel of >=30 will have a 1 hour cert
#     #   - "40:10" # >=40 will have a 10 hour cert
```

Nothing implements it. It is recorded here because it shows the shape was
wanted before the engine existed, and because "highest without going over
wins" is a different precedence rule from the one this design settles on. See
the ordering decision.

## What exists today (verified)

| Piece | State | Where |
| --- | --- | --- |
| Operator-named extra claims, captured at login | Built. `Extra map[string]string` maps a template name to a claim name. Nothing in the server knows a claim's meaning. | `server/config/types_oauth.go:47` |
| Numeric claims surviving extraction | Built, but flattened. `float64` is formatted to a decimal string and stored as one. | `server/service/auth.go:329` |
| Tiered lifetime by group, clamped to a ceiling | Built. Ordered tiers, first match wins, `min(tier, source, ceiling)`. | `server/service/lifetimepolicy.go` |
| Per-type gate, extensions, and approval flow | Built, single-valued. `requireGroup` is one string per certificate type. | `server/service/certtypepolicy.go` |
| Numeric predicates over a claim | **Absent.** No comparator, no typed accessor, no grammar. | n/a |

Two facts about the surrounding code matter to the design and are easy to
miss:

- **An admin authorization model now exists** (`config.Admin.RequireGroup`,
  `AuditorGroup`, `server/config/types.go:197`). The
  "there is no admin concept" prerequisite recorded in
  certificate-lifetime-policy.md is stale, which unblocks step 3 of that
  document's own roadmap.
- **Only three certificate types exist**: `user`, `service`, `pam`
  (`server/model/enums.go:17`). Host certificates are dead. Comments in
  `server/config/types_certificates.go:107` and `server/service/keyid.go`
  still reference a host type and a `CertOptions.KeyIDTemplate` field that
  does not exist. Unrelated cleanup, noted so the next reader does not chase
  it.

## Findings

Six findings, all verified against `023c0a8`. F3, F4 and F6 are pre-existing
defects independent of this proposal; they are listed here because a more
dynamic policy amplifies each of them.

### F1. Scores compare as strings, and the ordering is wrong

`extraClaims` renders a numeric claim through `strconv.FormatFloat`, so a
Level of Confidence of 40 reaches policy as the string `"40"`. Compared as
strings, `"9" >= "40"` is **true**: lexicographic order puts 9 above 40. A
policy built on string comparison would grant the longest lifetimes to the
lowest scores, in exactly the single-digit range where it matters most.

Template comparison does not rescue this. `extraValue` is a struct, so
`text/template`'s `ge` and `eq` reject it outright with "invalid type for
comparison".

Any numeric policy needs a typed numeric path: a parsed accessor decided at
extraction, not a string comparison at evaluation.

### F2. Extras are not hydrated at the authorization gate

The session identity carries no `Extra`; it is re-read from the user's row
during approval. That read happens *after* the gate deciding whether the
caller may approve this certificate type at all:

| Line | Step | Claim state |
| --- | --- | --- |
| `certrequest.go:563` | Narrow requested options against the type's extensions | not hydrated |
| `certrequest.go:564` | `checkApproverAuthorization`, the type gate | **not hydrated** |
| `certrequest.go:568` | `bindRequester` calls `resolveUser`, reading the users row | row in hand |
| `certrequest.go:577` | `identity.Extra = decodeExtraFields(user.ExtraFields)` | hydrated |
| `certrequest.go:721`, `:1004` | `evaluateDuration` | hydrated |

So lifetime and extension policy can read a claim today. The type gate
cannot.

The reordering is constrained: the gate must stay ahead of `bindRequester`,
so a caller who cannot approve never claims the request. The clean seam is
`resolveUser`, already a separate method. Call it before the gate to hydrate,
and leave the binding where it is.

### F3. A zero `default_duration` yields a zero-second certificate

The config comment on `LifetimePolicy.DefaultDuration` states that a zero
value falls back to the enclosing `ValidDuration`. It does not. With tiers
configured, no `default_duration` set, and an identity matching no tier, the
engine returns `0s`.

Verified empirically: a probe returned
`duration=0s reason="no tier matched, default: 0s"`.

It fails closed rather than open. The signer rejects a certificate whose
`ValidBefore` is not after `ValidAfter` (`server/signer/sign.go:133`), so the
outcome is a failed issuance carrying an error about certificate validity,
several layers from the config line that caused it. No test covers this case.

This matters here because claim conditions multiply the number of "nothing
matched" paths.

### F4. The reason a lifetime was shortened is computed, then discarded

`evaluateDuration` builds a human-readable `narrowingReason`
(`lifetimepolicy.go:127`) and both call sites throw it away with `_`
(`certrequest.go:721`, `:1004`). It has zero consumers repo-wide. Nothing on
the approval page or the decision record says why a certificate got the
lifetime it got, and the API surfaces only the static configured ceiling
(`webtypes.go:369`), never the effective value.

certificate-lifetime-policy.md asks for this twice: "record the decision, not
just the log line", and step 2 of its own roadmap. Under group tiers it is a
gap. Under claim conditions it is the difference between a policy an operator
can audit and one they can only observe.

### F5. A claim is only as fresh as the last login

`Extra` is written to the users row at login and read back at approval. A
background investigation that *lowers* someone's score has no effect until
they authenticate again.

For service enrollments the freeze is longer and deliberate: lifetime is
evaluated once at enrollment, and the code stays redeemable for
`enrollment_duration`, defaulting to `8760h`. A withdrawn clearance can keep
minting certificates for a year.

This is not a bug. It is the existing evaluate-at-enrollment-time contract
meeting an attribute that contract never anticipated. Group membership has
the same staleness, but nobody frames a group as a trust score, so the gap
goes unnoticed. Naming a claim "Level of Confidence" changes what operators
will assume the number is doing. See the freshness decisions below.

### F6. An empty `extensions` list means opposite things at two levels

At the type level, an empty permitted list denies everything: `intersectStrings`
returns `nil` when nothing is permitted (`certrequest.go:1088`). At the
source-rule level the guard is `if len(rule.extensions) > 0`, so an empty
list skips the intersection entirely and applies **no restriction**.

The config comment documents the opposite: "An empty list means no extensions
(equivalent to an explicit 'no extensions' policy); omit this field to apply
no extension restriction." That distinction is not implemented and cannot be
as written, because an omitted field and `extensions: []` are both length
zero at the only place the code checks.

Verified empirically: a rule with `extensions: []` against a request for
`[permit-pty, permit-agent-forwarding]` returned **both**.

The grammar below retires this rather than patching it. See the extensions
model.

## The model

### Conditions

A tier matches on a condition. The grammar is deliberately closed; six forms,
and no more:

- `group: <name>`, membership, exactly today's behaviour.
- `claim: <name>` with `at_least` / `at_most`, numeric.
- `claim: <name>` with `exactly`, numeric equality, desugaring to
  `at_least: N` and `at_most: N` so there is no second comparison path.
- `claim: <name>` with `equals` / `one_of`, scalar equality or scalar against
  a set.
- `claim: <name>` with `contains`, membership in a list-valued claim.
- `all_of: [...]` and `any_of: [...]`, one level of nesting, not arbitrary
  depth.

No negation in the first cut: `not` inverts the fail-closed reasoning on an
absent claim, which is the property most worth keeping simple.

No arithmetic, no weights, no summing. That restriction is load-bearing; see
[Deliberately rejected](#deliberately-rejected).

### Config shape

```yaml
# The claim is named once, by the operator. Unchanged from today.
authentication:
  fields:
    extra:
      loc: level_of_confidence

cert_options:
  user:
    valid_duration: 8h            # ceiling. Nothing below may exceed it.
    allowed_extensions:           # ceiling. A grant outside this is a startup error.
      - permit-pty
      - permit-agent-forwarding
      - permit-port-forwarding

    # Replaces require_group outright. Nothing has shipped, so no alias.
    require:
      all_of:
        - group: "SSH Users"
        - claim: loc
          at_least: 20

    lifetime_policy:
      on_absent_claim: floor      # floor, never "ignore"
      default_duration: 15m
      default_extensions: []      # start from nothing. Grants are opt-in.
      tiers:                      # FIRST MATCH WINS. Order is the admin's job.
        - name: cleared           # names the row for the audit record
          when: { claim: loc, at_least: 40 }
          max_duration: 8h
          grant_extensions: [permit-pty, permit-agent-forwarding, permit-port-forwarding]
        - name: standard
          when: { claim: loc, at_least: 30 }
          max_duration: 1h
          grant_extensions: [permit-pty]
        - name: probation
          when: { claim: loc, exactly: 25 }
          max_duration: 30m
          grant_extensions: [permit-pty]
        - name: contractor        # no grant_extensions, so default_extensions,
          when: { group: contractors }   # which is [] here. Grants nothing.
          max_duration: 30m
```

Service certificates use the same grammar with one addition,
`max_enrollment_duration`, tiering the enrollment code's own lifetime
alongside the certificate's:

```yaml
cert_options:
  service:
    valid_duration: 24h                 # ceiling for each certificate
    enrollment_duration: 8760h          # ceiling for the code
    lifetime_policy:
      default_duration: 1h
      default_enrollment_duration: 720h
      tiers:
        - name: cleared-owner
          when: { claim: loc, at_least: 40 }
          max_duration: 24h
          max_enrollment_duration: 8760h   # a year of unattended renewals
        - name: standard-owner
          when: { claim: loc, at_least: 30 }
          max_duration: 1h
          max_enrollment_duration: 720h    # 30 days, then re-approve
```

### The algebra

```
granted     = tier.grant_extensions ?? default_extensions        (starts empty)

duration    = min( valid_duration, tier.max_duration, source_rule.max_duration )
enrollment  = min( enrollment_duration, tier.max_enrollment_duration )
extensions  = requested & allowed_extensions & granted - source_rule.removed_extensions
allowed     = require(identity) && linkage(identity, selection)
```

### Invariants

- **Nothing a tier states can exceed the ceiling.** A high score cannot lift
  a certificate above `valid_duration`, and a `grant_extensions` entry outside
  `allowed_extensions` is a startup error rather than a silent trim. A tier
  states a value; the type's ceiling bounds it. One rule for both axes.
- **An absent claim is never neutral.** It resolves to the floor. It must
  never mean "skip this condition", which is how a missing claim becomes the
  most generous outcome.
- **An unparseable claim is an absent claim.** A score arriving as `"high"`
  against an `at_least` comparator takes the absent path, loudly. It must not
  take the zero path silently.
- **Every referenced claim must be declared.** A `claim: loc` with no `loc` in
  `authentication.fields.extra` is a startup error. This keeps the claim name
  out of the server while still catching a typo before it becomes a runtime
  surprise, and it is the posture the mail templates already take.
- **No key may need to distinguish "omitted" from "empty".** F6 exists because
  `len(x) == 0` is asked to carry two opposite meanings. Both keys retire the
  problem: an empty or omitted `grant_extensions` grants nothing, an empty or
  omitted `removed_extensions` removes nothing, and in each case the two
  spellings agree.
- **A zero or missing `default_duration` is a startup error**, closing F3
  rather than inheriting it.

### Why granting does not break narrowing-only

A tier that *grants* reads like widening, and the project's hard constraint
forbids that. The constraint does not apply here, and its wording is precise
about why:

> Nothing reachable **over HTTP** may make issuance more permissive than the
> loaded configuration allows.

That governs the boundary between the configuration and everything at
runtime. It says nothing about how the configuration composes internally, and
it cannot, because the config file is the authority the constraint is defined
against.

The invariant holds unchanged and gains a startup check that makes it
visible: a `grant_extensions` entry outside `allowed_extensions` fails
startup. The ceiling is still the outer bound, still set only by the config
file.

**Identity grants, network narrows.** Source rules keep a subtractive key and
keep narrowing only. That is not an inconsistency, it is the zero-trust
position stated in config: being on the office range is not a reason to
receive a capability the tier withheld. The runtime-editable rules
certificate-lifetime-policy.md plans stay narrowing-only for the same reason,
so the three layers compose in one direction: config grants, network narrows,
runtime narrows.

## Decisions, and the reasoning behind each

Flagged **[judgement]** where the decision rests on a call about operator
behaviour or deployment shape rather than a fact about the code. Those are
the ones to re-open first if circumstances changed.

### Freshness: login-freshness, accepted **[judgement]**

A lowered score takes effect at the subject's next login. No
re-authentication, no revalidation.

*Reasoning:* the alternatives cost more than the exposure is worth at this
stage. *Consequence:* the bound is real and belongs in config docs rather
than being discovered. Surfacing the claim's capture time on the approval
page costs little and lets an approver see how old the number is.

### Service enrollments: locked at approval **[judgement]**

No re-evaluation at retrieve. Lifetime and conditions are frozen when the
enrollment is approved, consistent with the existing evaluate-at-enrollment-time
contract.

*Reasoning:* an unattended job that silently drops tier has no human present
to see why. *Consequence:* combined with login-freshness, a code can outlive a
withdrawn clearance by up to `enrollment_duration`. The lever against that is
`max_enrollment_duration`, below, which shortens the exposure without
re-evaluating anything at retrieve.

### Absent claim: floor, on every axis

A missing or unparseable claim resolves to the floor, at the type gate as
well as on lifetime. One `on_absent_claim` key, one meaning.

*Reasoning, and this is the part worth preserving:* total denial is the
identity provider's job. An identity that should not reach ssoosshd at all is
expected never to be issued a token. Conditions here shape what an
already-admitted identity receives; they are not a second front door. This
retired an earlier proposal for a separate `deny` posture at the gate.

### Comparison: real, not integer

Compare as `float64`, matching what JSON decoding already produces.

*Consequence:* boundaries are inclusive, non-finite values are rejected at
extraction, and `exactly` carries a caveat. `exactly: 40` against `40.0`
matches; against `39.9999` it does not, and nothing says why. It is the right
operator for an integer-valued score and the wrong one for a computed
confidence. A startup warning when the configured literal is not integral
would be worth having; not an error, since integer-valued claims are the
normal case.

### The score may appear in key IDs

`{{.Extra.loc}}` in a key ID template is fine and needs no special handling.
It already works today.

*Reasoning:* the score is not protected information in the deployment this
was designed against. **[judgement]** A deployment where it is would want the
opposite, because key IDs land in `auth.log` on every target host, making the
score readable by every administrator of every machine the person logs into.
*Consequence:* the key ID template and the policy engine now read the same
claim name, so renaming it in `authentication.fields.extra` breaks both. An
argument for the startup check that every referenced claim is declared.

### Tier ordering: the admin owns it **[judgement]**

Neither startup validation nor best-match reordering. First match wins, the
list means what it says, and a misordered list is the administrator's mistake
to make.

*The problem being accepted:* numeric thresholds are nested by construction.
Everyone satisfying `at_least: 40` also satisfies `at_least: 30`. Write those
rows in ascending order and every cleared user silently takes the shorter
tier. No error, no warning, and the certificate looks normal.

*Reasoning:* two things make it survivable. The blast radius is bounded by
the ceiling, not by the ordering, so a misordered list can put someone in the
wrong tier but never above the type's bound. And the natural mistake, writing
thresholds ascending while durations descend, under-grants: it reads as "my
clearance is not being honoured", which is a complaint someone files, not a
breach nobody notices. It also keeps a single evaluation model for mixed
group-and-claim lists, with no precedence rule to invent between "most
specific number" and "first matching group".

*Note the divergence:* certificate-lifetime-policy.md chose the opposite for
source rules ("longest prefix wins, ties resolve to the stricter rule.
Order-independent, which first-match-wins is not, and that matters once rows
are editable"). Tiers deliberately do not follow it. If runtime-editable
policy is ever built, re-open this: the argument that order-independence
matters once rows are editable applies to tiers too.

*Obligation this creates:* documentation, not code. The config sample must
say that tier order is significant and numeric thresholds belong in
descending order. A non-blocking startup warning would sit inside this
decision rather than against it.

### Policy explanation: a JSON document on the decision record

`certificate_request_decisions` is still modifiable, takes no foreign keys,
and can hold JSON. The explanation goes there as a structured document, not a
free-text string.

*Reasoning:* the shape can carry the winning tier's name, the condition it
matched, the source rule, the computed duration and the ceiling that bounded
it, without a second migration each time policy grows an axis. Structure it
as a document from the start; a flat reason string is the thing that would be
regretted. The tier `name` field exists for this.

### Config layout: `require_group` is dropped, not aliased

`require` replaces `require_group` outright.

*Reasoning:* nothing has shipped, so there is no compatibility to preserve.
*Consequence:* the "what if both are set" question disappears, and so does
the startup error guarding it. Scoped to the per-type certificate gate;
`admin.require_group` and `admin.auditor_group` are a different surface and
are untouched.

### PAM: out of scope entirely

Not just lifetime tiering; all of it. `CertOptionsPAM` is left exactly as it
stands, with its own `require_group`, `valid_duration` and `extensions`, none
of them touched by the condition grammar.

*Reasoning:* PAM exists to mint a certificate carrying the user principal
plus shared accounts, so a PAM stack can confirm the person is who they claim
to be. That is an identity assertion, not a capability grant, and policy has
nothing to shape. An earlier suggestion to extend `require` across PAM for
consistency was withdrawn: consistency is not a reason to give a type a
mechanism it has no use for.

### Service policy stays approver-keyed **[judgement]**

Tiers are evaluated against the approver, while the certificate's principal
is the service account. The same account approved by two people gets two
different lifetimes.

*Reasoning:* the approver is already load-bearing on this path. `require`
gates who may approve, and `checkServiceAccountLinkage` already restricts the
choice to accounts that approver is entitled to, so reading a tier as "how far
may this person vouch" fits the mechanism that exists. The shopping incentive
is accepted; tiers are not expected to carry much weight here, and the wiring
is nearly free because the engine is already called on that path with the
approver's identity. Build it because the lift is small, not because the
tiering is load-bearing.

*The alternative, if this is re-opened:* key service policy on the account
rather than the person. No attribute of a service account exists anywhere in
the system to key on, so that is a data-model question, not a policy-engine
one.

### `max_enrollment_duration`: wanted

The enrollment code's own lifetime becomes a tiered value, clamped by
`cert_options.service.enrollment_duration`.

*Reasoning:* it is the lever against a code outliving the conditions that
authorized it, and it works without re-evaluating anything at retrieve, so it
sits inside the locked-at-approval decision rather than against it.

## Deliberately rejected

### Scoring computed by the server

certificate-lifetime-policy.md records a scoring model that was designed and
then superseded, for two reasons that still hold: an outcome reached by
arithmetic over several partial matches cannot be explained by pointing at
one rule, and weights cannot be edited safely in a UI because changing one
silently moves every subject across every threshold.

A Level of Confidence looks adjacent to that, and the distinction is what
makes this design survivable:

- The rejected design had **the server compute the score**, summing weights
  across trusted subnets, untrusted subnets and reverse-DNS patterns. The
  number was an artifact of configuration, so no row meant anything alone.
- Here the number **arrives whole, from the IdP**, as one authoritative input.
  The server performs a comparison, not a computation. One tier wins, it has a
  name, and that name is the answer to "why one hour".

**The line to hold: the server never does arithmetic on the score.** The
moment policy grows `weight:` or adds two claims together, this becomes the
design that was already rejected, with the same explainability problem and
the same reason it could not be given a UI.

### `text/template` as the policy language

The obvious shortcut is reusing the template engine already in the tree:
`when: '{{ge (num .Extra.loc) 40}}'`. Wrong tool, and the codebase already
shows why by using it correctly elsewhere.

Key ID templating *renders*: the output is a string, no security decision
depends on it, a missing value degrades to `MISSING`, and startup executes
every template once against zero values to catch typos. That works because
there is no right answer to get wrong. A policy predicate has one.

- A template error becomes a policy outcome. A failed predicate render has to
  resolve to allow or deny, and neither is obviously correct.
- The startup dry-run stops working. Executing against zero values proves a
  key ID template is well-formed; it proves nothing about whether a predicate
  is right, and `missingkey=zero` actively hides the absent-claim case the
  invariants depend on catching.
- An arbitrary expression is not a row an administrator can read and approve,
  which forecloses the runtime-editable policy already being built toward.
- It reopens F1, since comparison operators over an untyped value are exactly
  where `"9" >= "40"` lives.

Templates remain right for anything that renders. If `force-command` ever
gains a bound, that is template-shaped. Gating is not.

### Folding `group:` into `claim:`

Tempting: one form instead of two, with `group: contractors` becoming
`claim: groups, contains: "contractors"`. Rejected for three reasons.

- **Groups and extras are not stored in the same place.** `Identity.Groups` is
  carried by the session and has no column on the users row; `upsertUser`
  writes username, email, other_accounts, service_accounts and extra_fields,
  and nothing else. `Identity.Extra` is persisted and re-hydrated from that
  row at approval. So `claim: groups` cannot resolve through the extras map
  without a new column and a migration. This is also why group gating works
  at the authorization gate today while claim gating does not (F2).
- **`equals` is the wrong operator for a list.** Groups is a `[]string`.
  Comparing it with `equals` either means "this list is exactly that list" or
  quietly degrades into membership. Unifying properly needs `contains`, which
  is one more form in the grammar, not one fewer.
- **The common case gets harder to read**, and it couples every tier to the
  *name* of the groups claim, so changing `fields.groups` would mean editing
  every rule mentioning it.

**What is shared is the comparator and combinator layer, not name
resolution.** `group:` stays a distinct condition variant evaluated directly
against `Identity.Groups`; `claim:` resolves through `Identity.Extra`; both
feed the same comparators and the same `all_of` / `any_of`.

An earlier draft proposed a general synthetic-name registry so identity
fields could be addressed as claims. That is withdrawn: `other_accounts` and
`service_accounts` are principal and linkage machinery, deciding which names
a certificate may carry rather than how long it lives or what it may do, and
are not policy inputs. With groups the only identity field a condition would
ever reach outside the extras map, a registry would serve exactly one
binding. A two-variant condition type is the smaller thing, and it needs no
name-collision rule at startup.

## Sequencing

Not an implementation plan; the order the pieces have to land in, and why.
Steps 1 to 3 are prerequisites, and the first is worth doing regardless of
whether the rest is ever built.

1. **Fix what is already broken.** Make a zero `default_duration` a startup
   error (F3); record the policy explanation as a JSON document on
   `certificate_request_decisions`, structured from the start (F4); and
   resolve the empty-list ambiguity in source rules by flipping them to a
   removal key (F6). All three are pre-existing and small, and together they
   make policy outcomes explainable and honest before policy gets harder to
   explain.
2. **Give `extraValue` a typed numeric accessor** (F1), deciding the parse at
   extraction rather than at evaluation. Everything numeric rests on this.
3. **Move hydration ahead of the gate** (F2): `resolveUser` before
   `checkApproverAuthorization`, binding unchanged. Unblocks the type-gating
   axis.
4. **Add the condition grammar to `tiers`**, lifetime axis only, with `group:`
   preserved and `grant_extensions` as the tier-level key. One startup check:
   a grant outside `allowed_extensions` is rejected. Tier ordering is
   deliberately not validated. Service certificates inherit this for free,
   since the engine is already called on that path with the approver's
   identity.
5. **Replace `require_group` with `require`** for the type-gating axis, once
   conditions have proven out on the lower-risk lifetime path. User and
   service only; PAM keeps its own `require_group` untouched.
6. **Tier the enrollment code's lifetime** with `max_enrollment_duration`, the
   only service-side clock the engine does not already reach.
7. **Document the freshness bound.** F5 is decided, not deferred. What remains
   is writing down, in config docs, that a score is only as fresh as the last
   login and that an enrollment code holds its conditions for
   `enrollment_duration`.

Nothing here preserves compatibility, because nothing has shipped. The one
thing to hold across the sequence is that each step leaves the ceiling
semantics intact, since that is the boundary the whole design rests on.

## Provenance: what was verified and how

Verified against `023c0a8` on 2026-08-24, by reading source and by executing
three throwaway probes. The probes were scratch-only and the working tree was
left clean; none of them are in the repository.

| Claim | How it was established |
| --- | --- |
| F1, string comparison and template rejection | A `text/template` harness against a mirror of `extraValue`. `ge .Extra.loc 40` returned "invalid type for comparison"; `ge "9" "40"` returned `true`. |
| F2, hydration ordering | Reading `certrequest.go:563-577` and the `evaluateDuration` call sites. |
| F3, zero-duration path | A `lifetimePolicyEngine` probe: tiers configured, `default_duration` omitted, no tier matching. Returned `duration=0s`. |
| F4, discarded reason | Repo-wide grep for `narrowingReason`: two definitions, zero consumers. |
| F5, freshness | Reading `upsertUser`'s column list and `EnrollmentDuration`'s doc comment. |
| F6, empty-list semantics | A probe across both narrowing levels. Source rule with `extensions: []` returned both requested extensions; type level with an empty permitted list returned `[]`. |
| Only three certificate types | `server/model/enums.go:17-19`, and `newCertTypePolicies`' map. |
| Admin model exists | `server/config/types.go:197-233`. |

**What to re-verify first when picking this up:** whether F3, F4 and F6 are
still present. They are independent defects that may have been fixed on their
own, and if they have been, step 1 shrinks or disappears.

## To resume this

1. Re-run the checks in the table above against the current tree, and update
   the `file:line` anchors.
2. Re-read [Decisions](#decisions-and-the-reasoning-behind-each), paying
   attention to the **[judgement]** items. Those rest on calls about operator
   behaviour and deployment shape rather than facts about the code, and are
   the ones most likely to have aged. Tier ordering and the key-ID disclosure
   call are the two where a different deployment would reasonably decide
   differently.
3. Confirm the model still fits the code, in particular that
   `lifetimePolicyEngine` still owns evaluation and that the approval path
   still hydrates `Extra` where F2 describes.
4. Then turn this into an implementation plan against the sequencing above.
