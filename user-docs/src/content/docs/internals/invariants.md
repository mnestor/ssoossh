---
title: Invariants
description: Rules the ssoossh code depends on, each one load-bearing somewhere else.
eyebrow: Internals
sidebar:
  order: 1
---

Rules the code depends on. Each one is load-bearing: something elsewhere is
written assuming it holds, so changing one is a design decision, not a
refactor. Code comments cite this file by name.

For what ssoossh deliberately does not do, and why, see
[Decisions](/ssoossh/project/decisions/). That page answers "why is there no
X"; this one answers "what may I rely on". For the operator-facing version of
the same rules, see the [security model](/ssoossh/concepts/security-model/).

## Issuance

**Server config is the outer bound.** Nothing reachable over HTTP can make
issuance more permissive than the loaded configuration allows: not
longer-lived, not more capable. Client requests *ask*, the web UI *narrows*,
server config *gates*. Options a deployment does not permit are trimmed rather
than rejected, so both the requested and the narrowed sets are carried
together and the approval page can show what was asked for alongside what
would actually be granted. A human authorizing issuance has to see what they
are authorizing.

Standing policy sits in the same shape, with the administrator in the
"narrows" position: policy may only ever reduce what the config file already
allows. See
[Certificate lifetime policy](/ssoossh/operations/certificate-policy/).

**Every issuance is approved by a human in the browser**, then signed. There
is no path that turns a request into a certificate without passing through the
sign queue.

**`no-touch-required` is granted only for service certificates**, and only for
an enrolled hardware-backed `sk-` key. Never for client-generated keys.
**`verify-required` is never used.**

**There is no host certificate type**, and no secure host-verification
mechanism to justify one. `ssoossh host` is local principal-mapping tooling
for `AuthorizedPrincipalsCommand` and has no server side. Do not add a host
type without solving host verification first.

## Keys

**Private keys never leave the machine that generated them.** The server never
generates or stores a private key; it records public key material and issuance
metadata only. The client manages its own keypair.

**The CA key is reached through a `CAKeySource` interface**, never hardcoded
to one backend. The config file and a PKCS#11 token are the sources behind
that seam, which is also what lets the signer run as a separate,
minimally-privileged process.

## Identity

**Group membership never appears in a certificate, and persisted group rows
are never an authorization input.** Authorization -- admin, SOC, auditor, and
the certificate policy gates -- is evaluated from the session identity only.
Groups can change between sessions, so `model.User` keys history to a stable
subject identifier rather than storing claims.

`user_groups` is a snapshot for notification fan-out and display: it answers
"who should this reach", never "may this caller do this". It was added by
directory sync (see [LDAP enrichment](/ssoossh/operations/ldap/)), which
retired the older, stronger wording that group membership is never persisted
at all. What survives is the half that was load-bearing -- nothing reads those
rows to make a decision, and per-request database hydration of identity fields
remains out of scope, since the session carries them.

**Session and authorization headers are never captured.** The headers recorded
on an approval decision are a deliberate allowlist -- `User-Agent`,
`Accept-Language`, `X-Forwarded-For` -- not "every header minus a denylist".
`Cookie` carries the live session token; neither it nor `Authorization` is
ever written to an audit record.

**Client-supplied addresses never feed policy.**
`RequestedOptions.SourceAddresses` is unverified. The address policy consults
is the one the server observed.

## Client

**The client never opens a listening port.** No loopback redirect, no local
callback server. The browser approval flow works without one.

## Package boundaries

**`client/` and `pam_ssoossh/` may not import `server/`, or each other.**
Anything shared travels through `internal/` -- `internal/api` for the HTTP
client, `internal/apitypes` for the wire contract. This is why
`apitypes.CertificateRequestStatus` duplicates `model.CertificateRequestStatus`
rather than importing it; `server/model` remains the source of truth for the
database, and [Wire types](/ssoossh/internals/wire-types/) covers how the two
are kept from drifting.

**`internal/` may not import back up into `client/` or `server/`.** A caller
maps its own config into whatever the internal package defines, which is why
`api.Config` exists rather than internal code taking a `client/config.Config`.
