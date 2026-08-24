# Decisions

The record of what ssoossh deliberately does not do, and why. If you are
about to ask "why doesn't it just…", the answer is probably here. Decisions
are re-openable — but with a reason the circumstances changed, not by
re-asking.

What ssoossh *does* do is in [features.md](features.md).

## Security invariants

Hard constraints every change is measured against, from the original design
brief:

- The server never receives private keys. The client sends private keys
  nowhere except the local ssh-agent or a local file.
- The client never opens a listening port. No loopback redirect; the
  browser lands on the server and the client learns the outcome over its
  SSE stream.
- Server config is the outer bound on every option. Client request asks,
  web UI narrows or adjusts, server config gates. Options the deployment
  does not permit are trimmed (not rejected) and shown in the web UI
  before approval.
- Group membership never appears in a certificate. Groups feed the
  lifetime decision only.
- `verify-required` is not used at all. `no-touch-required` is not offered
  for client-generated keys, only relevant for enrolled `sk-` keys on the
  service path.

## Parked on purpose

Worth doing eventually; not started because nothing needs it yet.

- **Tighter rate limiting on service-enrollment redemption.** The retrieve
  endpoint has a per-code rate limit; a stricter scheme may be warranted
  once service enrollment sees real unattended use.
- **A dedicated signer configuration file.** The signer process reuses the
  full server config with mode-aware validation. That means secrets the
  signer never uses can sit in its config file — acceptable for now,
  worth hardening once the split topology is in real use.
- **Re-checking group claims mid-session.** Groups are read at login and
  the session's absolute cap bounds how stale they can get. If a shorter
  revocation window is ever needed, the fix is re-validating claims on
  session refresh — not caching group state in the database, which drifts
  from the identity provider.

## Declined: wrong for this product

- **API versioning, gateways, service meshes, circuit breakers.** One
  self-hosted binary with a single first-party client under active
  development. There is nothing to version against and nothing to mesh.
- **Internationalization.** Single-locale self-hosted tool. Revisit when
  the community asks for it.
- **Certificates that outlive their approval.** Issued certificates are
  never persisted server-side. Delivery is the only copy; a client that
  misses it re-requests. This is a feature: there is no certificate store
  to steal.

## Declined: security decisions with teeth

- **Host certificates.** A host certificate asserts "this key speaks for
  this hostname," but nothing in the design could verify that claim: approval
  reduced to a human eyeballing a hostname string an unauthenticated
  requester typed, and every scheme for server-side principal mappings
  had the same hole one layer down. Issuing unverifiable host identity
  from the CA that also signs user access is worse than issuing none.
  Revisit only with a real host-verification mechanism — something like
  an ACME challenge proving control of the name — not by re-arguing the
  approval flow. Local principal mapping for
  `AuthorizedPrincipalsCommand` stayed in the client (`host mapping`,
  `host principals`); it never needed a server.
- **An admin role stored in the database.** Admin is an OIDC group named
  in configuration. A database flag has a bootstrapping problem ("who
  creates the first admin"), drifts from the identity provider, and lets
  an admin promote others through the API. Nothing reachable over HTTP can
  widen anyone's authority — that property is the point.
- **Runtime-editable policy that can loosen anything.** Policy edited
  through the web tier may only ever narrow. A compromised web tier can
  therefore deny service, but cannot obtain a certificate the config file
  would not already have allowed.
- **Encrypting signing-job payloads to authenticate the sender.**
  Encryption does not authenticate, the payload is not secret (it is the
  future certificate's own contents), and a fleet-shared key is coarse and
  awkward to rotate. The broker connection carries per-node identity via
  mTLS instead. Additionally the certificate itself is useless without 
  the private key that never left the client.
- **Pinning user certificates to a source address.** People move — office,
  VPN, hotel, phone tether — and a pinned certificate turns every network
  change into a failed login for no gain a short lifetime does not already
  provide. Services sit still, so service certificates *can* be pinned.
  This may be implemented at some point as yes, users do log into remote
  systems and need to ssh from them. So, pinning those certificates could
  be considered a worthwhile choice.

## Declined: durability and availability machinery

- **JetStream (durable delivery) for the signing pipeline.** NATS core
  with queue groups, at-most-once. A dropped signing job means the person
  who just approved sees their terminal still waiting, cancels, and reruns
  login — the flow is short and interactive, so the human is the retry
  mechanism, and durability machinery buys nothing worth its complexity.
- **Sticky sessions as a multi-instance workaround.** Request-ID routing
  would fix certificate delivery but not the stranded-request sweep or
  per-process session keys — a configuration that half-works is worse than
  a clear requirement. Multi-instance requires NATS, full stop.
- **A "0 means expiry is disabled" request timeout.** Every consumer
  would need a special case for it and each is a hazard — a sweep with no
  bound, a cache with no safe eviction age. Startup rejects a non-positive
  `cert_options.client_timeout` instead.
- **Separate knobs for the approval window and the signing grace.** One
  budget, `cert_options.client_timeout`, with both shares derived from it.
  Two knobs could not say which phase each bounded, and the number an
  operator actually cares about — how long a client can hang — was
  neither of them but their sum, counting the signing share twice because
  the sweep interval derives from it as well.
