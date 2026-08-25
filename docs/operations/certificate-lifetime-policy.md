# Certificate lifetime policy

How long an issued certificate lives, and which options survive, derived from
the requester's group membership and the network they came from.

Implemented in `server/service/lifetimepolicy.go` and evaluated during
approval. Applies to **user and service** certificates only; PAM certificates
keep the flat `valid_duration` from their own config block.

For the open design work this leaves unfinished, see
[proposals/certificate-lifetime-policy-rework.md](../proposals/certificate-lifetime-policy-rework.md).

## The rule

Policy may only ever **narrow**. Nothing reachable over HTTP can make issuance
more permissive than the config file already allows:

- shorter lifetime, never longer
- fewer extensions (intersection with the type's configured set)
- more critical options, on the service path only

Adding a critical option reads like widening and is the opposite: it can only
prevent uses that would otherwise be allowed.

The final duration is always clamped to the type's `valid_duration` ceiling.

## Configuration

```yaml
cert_options:
  user:
    valid_duration: 10h            # the ceiling; policy only reduces from here
    lifetime_policy:
      default_duration: 10h        # when no tier matches, or no tiers configured
      tiers:                       # optional; omit for a flat default_duration
        - group: contractors
          max_duration: 1h
        - group: engineers
          max_duration: 10h
      source_policy:
        - cidr: 10.0.0.0/8         # office and VPN
          max_duration: 10h
        - cidr: 192.168.0.0/16     # lab
          max_duration: 4h
        - cidr: 0.0.0.0/0          # everywhere else
          max_duration: 15m
```

`lifetime_policy` is available under `cert_options.user` and
`cert_options.service`. An unparseable CIDR fails startup rather than
degrading at runtime.

## How a duration is chosen

1. **Tier**: the **first** tier whose group appears in the requester's groups.
   Order matters here. No match means `default_duration`.
2. **Source rule**: the **longest-prefix** match against the request's source
   address, as in a routing table. Order does not matter. Ties resolve to the
   stricter rule, so a duplicate can never loosen anything.
3. The result is the minimum of those, clamped to `valid_duration`.

The matching rule is reported alongside the decision, so "why did this
certificate get fifteen minutes" is answerable from the log rather than by
re-deriving the computation.

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
