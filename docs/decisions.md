# Decisions

The record of what ssoossh deliberately does not do, and why. If you are
about to ask "why doesn't it just…", the answer is probably here. Decisions
are re-openable — but with a reason the circumstances changed, not by
re-asking.

What ssoossh *does* do is in [features.md](features.md).

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
- **GraphQL, WebSockets, PWA/offline support, push notifications.** The
  web UI is a login and an approval page. The one real-time channel is
  SSE, one-directional by nature: the server telling a waiting client its
  certificate is ready.
- **Internationalization.** Single-locale self-hosted tool. Revisit only
  if community deployments actually ask.
- **Certificates that outlive their approval.** Issued certificates are
  never persisted server-side. Delivery is the only copy; a client that
  misses it re-requests. This is a feature: there is no certificate store
  to steal.

## Declined: security decisions with teeth

- **The deployment-wide pending-requests listing.** There used to be an
  endpoint listing every pending request to any signed-in user; it was
  deleted rather than admin-gated. A request has no owner at creation, the
  request ID is the capability, and a certificate takes the *approver's*
  principals — so any screen inviting people to approve requests they did
  not start is an escalation channel. Gating it admin-only would have
  concentrated that hazard on the most privileged accounts. Kept here as
  the shape of question to ask about any future listing endpoint.
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
  mTLS instead.
- **Pinning user certificates to a source address.** People move — office,
  VPN, hotel, phone tether — and a pinned certificate turns every network
  change into a failed login for no gain a short lifetime does not already
  provide. Services sit still, so service certificates *can* be pinned.

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
- **A keepalive poll to hold web sessions open.** The session slides on
  real activity server-side instead. A poll keeps a session alive for an
  unattended browser, which is exactly the case the idle timeout exists
  for.
- **`request_ttl: 0` as "expiry disabled."** Removed outright. Every
  consumer needed a special case for it and each was a hazard — a sweep
  with no bound, a cache with no safe eviction age. Startup rejects a
  non-positive TTL.
