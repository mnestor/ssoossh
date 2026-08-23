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
- **Automated PAM-stack end-to-end testing.** There is no test that
  installs `pam_ssoossh.so` into a real `pam.d` stack and drives
  `pam_authenticate` through it. Decided (2026-08-23) to accept and
  document that gap rather than automate it: the module's logic - argument
  parsing, the four certificate checks, fail-closed error mapping - is
  covered by the unit suite (`CGO_ENABLED=1 go test -tags=pam
  ./pam_ssoossh/...`), the build and exported PAM symbols are asserted in
  `test/pam/`, and the real-stack pass is a documented manual step using
  `pam_ssoossh/testing/pamtest.c` before touching a production `pam.d`.
  Three automation attempts showed the cost (container PAM stacks, syslog
  capture, a C conversation driver) out of proportion to what the unit
  suite does not already catch. Revisit if a PAM-integration bug ever
  actually escapes to a release.
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
- **`request_ttl: 0` as "expiry disabled."** Removed outright. Every
  consumer needed a special case for it and each was a hazard — a sweep
  with no bound, a cache with no safe eviction age. Startup rejects a
  non-positive TTL.
