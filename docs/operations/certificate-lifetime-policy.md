# Certificate lifetime policy

How long an issued certificate lives, and which options survive, derived from
what the approver's identity satisfies and the network the request came from.

Implemented in `server/service/lifetimepolicy.go` (with the condition grammar
in `server/service/policycondition.go`) and evaluated during approval. Applies
to **all three** certificate types. For PAM the gate is the axis that matters
in practice: duration tiers against a 30-second, validated-once certificate
and extension grants against an empty ceiling have nothing to do, so the
expected PAM configuration is `require` alone.

For the open design work this leaves unfinished, see
[proposals/certificate-lifetime-policy-rework.md](../proposals/certificate-lifetime-policy-rework.md).

## The rule

Policy may only ever **narrow**. Nothing reachable over HTTP can make issuance
more permissive than the config file already allows:

- shorter lifetime, never longer
- fewer extensions than the type's configured set, which is the outer bound
- more critical options, on the service path only

A tier may *grant* extensions, which reads like widening and is not: the
grant is bounded by the type's `extensions` ceiling, and a grant naming
anything outside that ceiling fails startup rather than being silently
trimmed. The constraint governs what is reachable over HTTP; the config file
is the authority it is defined against. **Identity grants, network narrows.**

Adding a critical option reads like widening and is the opposite: it can only
prevent uses that would otherwise be allowed.

The final duration is always clamped to the type's `valid_duration` ceiling.

## Configuration

```yaml
authentication:
  fields:
    extra:
      loc: level_of_confidence     # the claim is named once, by the operator

cert_options:
  user:
    valid_duration: 10h            # the ceiling; policy only reduces from here
    extensions:                    # the ceiling for grants
      - permit-pty
      - permit-agent-forwarding

    require:                       # who may approve at all
      all_of:
        - group: "SSH Users"
        - claim: loc
          at_least: 20

    lifetime_policy:
      default_duration: 15m        # required whenever a policy is configured
      default_extensions: []       # grants start from nothing when tiers exist
      tiers:                       # FIRST MATCH WINS; order is yours to get right
        - name: cleared            # names the row in the recorded explanation
          when: { claim: loc, at_least: 40 }
          max_duration: 10h
          grant_extensions: [permit-pty, permit-agent-forwarding]
        - name: engineers
          when: { group: engineers }
          max_duration: 8h
          grant_extensions: [permit-pty]
        - name: contractors
          when: { group: contractors }
          max_duration: 1h         # no grant_extensions, so grants nothing
      source_policy:
        - cidr: 10.0.0.0/8         # office and VPN
          max_duration: 10h
        - cidr: 192.168.0.0/16     # lab
          max_duration: 4h
          removed_extensions: [permit-agent-forwarding]
        - cidr: 0.0.0.0/0          # everywhere else
          max_duration: 15m
```

`require` and `lifetime_policy` are available under all three of
`cert_options.user`, `cert_options.service` and `cert_options.pam`. Service
certificates additionally tier the enrollment code's own lifetime with
`default_enrollment_duration` and per-tier `max_enrollment_duration`, both
clamped by `cert_options.service.enrollment_duration`.

An unparseable CIDR fails startup rather than degrading at runtime, and so
does a configured `lifetime_policy` with no `default_duration`: without one,
an identity matching no tier would receive a zero-second certificate that
fails later at signing, several layers from the line that caused it.

### Conditions

A tier's `when` and a type's `require` take the same closed grammar:

- `group: <name>` — membership, the pre-conditions behavior.
- `claim: <name>` with `at_least` / `at_most` — numeric, inclusive bounds.
- `claim: <name>` with `exactly` — numeric equality, desugaring to
  `at_least` and `at_most` of the same value.
- `claim: <name>` with `equals` / `one_of` — scalar equality, or against a set.
- `claim: <name>` with `contains` — membership in a list-valued claim.
- `all_of: [...]` / `any_of: [...]` — one level of nesting, no deeper.

Claims are compared as numbers, not strings. That is the whole reason for a
typed accessor: compared as text, `"9"` sorts above `"40"`, which would hand
the longest lifetimes to the lowest scores.

Every claim a condition names must be declared under
`authentication.fields.extra`, checked at startup, so a typo fails the
process instead of quietly failing the condition on every evaluation.

**An absent claim is never neutral.** A claim that was not captured at login,
or whose value cannot be used by the comparator (a word under `at_least`, a
list under `equals`, a scalar under `contains`), fails the condition and is
logged. It must never mean "skip this condition", which is how a missing
claim becomes the most generous outcome. `on_absent_claim` exists to state
that posture in config and accepts only `floor`.

Total denial is the identity provider's job. An identity that should not
reach ssoosshd at all is expected never to be issued a token; conditions here
shape what an already-admitted identity receives.

> **A score is only as fresh as the last login.** `Extra` is written to the
> users row at login and read back at approval, so lowering someone's score
> takes effect at their next authentication, not immediately. For service
> enrollments the freeze is longer and deliberate: conditions are evaluated
> once at approval and the code stays redeemable for its full lifetime, so a
> withdrawn clearance can keep minting certificates until the code expires.
> `max_enrollment_duration` is the lever against that.

## How a duration is chosen

1. **Tier**: the **first** tier whose `when` condition the approver's identity
   satisfies. Order matters here, and it is not validated: numeric thresholds
   are nested by construction, since everyone satisfying `at_least: 40` also
   satisfies `at_least: 30`. Write them in **descending** order. Ascending
   order silently lands every high-score identity in the shortest tier — no
   error, no warning, and the certificate looks normal. The blast radius is
   bounded by the ceiling, and the natural mistake under-grants, which someone
   complains about rather than nobody noticing. No match means
   `default_duration`.
2. **Source rule**: the **longest-prefix** match against the request's source
   address, as in a routing table. Order does not matter. Ties resolve to the
   stricter rule, so a duplicate can never loosen anything.
3. The result is the minimum of those, clamped to `valid_duration`.

The winning tier, the condition it matched, the source rule, the ceilings and
the effective values are recorded as a structured JSON document on the
approval's decision row (`certificate_request_decisions.policy_explanation`),
so "why did this certificate get fifteen minutes" is answerable from the
record rather than by re-deriving the computation.

Extensions follow their own algebra, in this order:

```
granted    = tier.grant_extensions ?? default_extensions   (starts empty)
extensions = requested & type.extensions & granted - source_rule.removed_extensions
```

The grant axis is active only when tiers are configured. Without tiers, the
type's `extensions` ceiling alone bounds a request, exactly as before.

**No source match means no reduction** — the type ceiling applies. Rules
*are* reductions, so the absence of one is "nothing to reduce". For a default
floor, write explicit `0.0.0.0/0` and `::/0` rules, the same idiom as a
firewall's default rule. The opposite reading (unmatched networks get the
strictest tier) is a reasonable guess and is not what happens.

IPv4 and IPv6 rules never match each other. Addresses are normalized with
`netip.Addr.Unmap()`, so an IPv4-mapped IPv6 form (`::ffff:10.0.0.1`) matches
a `10.0.0.0/8` rule instead of silently missing every rule.

## Which address

The address recorded on the request (`certificate_requests.source_ip`, from
`ClientIP()` at create time) — where the certificate will be *used*. Not the
approver's browser address, which is a different machine on a different
network.

`RequestedOptions.SourceAddresses` is client-supplied and unverified. It never
feeds policy.

> **Footgun.** Behind a reverse proxy with `http.trusted_proxies` unset, every
> client appears to come from the proxy's address — very likely an internal
> range, so *everyone* silently lands in the most generous tier, which is the
> exact opposite of the feature's purpose. ssoosshd warns at startup when a
> source policy is configured and `trusted_proxies` is empty. Do not ignore it.

`X-Forwarded-For` is ignored unless an operator explicitly trusts a proxy
range; `SetTrustedProxies` is always called.

## Source-address pinning

`pin_source_address: true` on a source rule pins the issued certificate to the
address it was requested from, so a stolen one is useless elsewhere.

**Service certificates only.** People move between office, VPN, hotel, and
phone tether; pinning a user certificate turns every network change into a
failed login, for no security gain that a shorter lifetime does not already
provide. Services sit still.

`force-command` is deliberately outside source policy entirely: "which
command" is not a property of a network.

> **Known defect.** `pin_source_address` is a field on the source *lifetime*
> rule, which conflates address restriction with lifetime capping, and what
> gets granted is the observed address rather than the configured network.
> This needs rework rather than a patch — see the
> [rework proposal](../proposals/certificate-lifetime-policy-rework.md).

## Service certificates and retrieval

A service certificate is signed at `retrieve`, potentially long after
approval and possibly from a different address. Policy is evaluated **at
enrollment time and frozen**, never re-derived at retrieve time. An unattended
job cannot silently drop into a stricter tier by being moved, because no human
would be present to see why issuance changed.
