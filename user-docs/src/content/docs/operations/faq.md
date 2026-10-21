---
title: Operator FAQ
description: Short answers to the questions people running ssoosshd ask, each pointing at the page with the detail.
sidebar:
  order: 15
---

Questions from people running the server. If yours is of the form "why
doesn't it just...", the [decisions](/ssoossh/project/decisions/) page exists
for exactly those.

## Is it production ready?

Early development. User, service, `sudo`/`su` PAM, and console certificates
work end to end, with the console-side PAM module shipped separately.
Interfaces and configuration are expected to change. The status table is on
[Roadmap](/ssoossh/project/roadmap/).

## Which identity providers work?

Any OIDC-compliant provider. The reference configuration uses
[pocket-id](https://github.com/pocket-id/pocket-id), and the walkthrough for
it is on [Identity provider](/ssoossh/operations/identity-provider/). There is
no provider-specific code.

## Can I run behind a reverse proxy (nginx, Caddy, Traefik)?

Yes, and it is the common case. Two settings must be right or login fails
silently: [`http.public_url`](/ssoossh/reference/config/http/#public_url), from
which the OIDC redirect URI and the CSRF origin check derive, and
[`http.trusted_proxies`](/ssoossh/reference/config/http/#trusted_proxies), or
`X-Forwarded-For` is ignored. One more thing to check that is easy to miss:
the proxy must not buffer the certificate event stream. See
[TLS and reverse proxies](/ssoossh/operations/tls-and-proxy/).

## Can I run behind a load balancer?

Yes. Run several `ssoosshd serve api` instances behind it, one or more
`ssoosshd sign` processes, NATS with mTLS as the message broker, a shared
PostgreSQL database, an explicit
[`http.cookie_key`](/ssoossh/reference/config/http/#cookie_key), and
[`multi_instance: true`](/ssoossh/reference/config/top-level/#multi_instance).
No sticky sessions are needed: approvals, certificate delivery, and web
sessions all cross instance boundaries. Procedure:
[Multi-instance and NATS](/ssoossh/operations/multi-instance/).

## Can I use NATS in single-server mode?

Yes, and there is a good reason to: CA key isolation. A single
`ssoosshd serve api` instance plus a `ssoosshd sign` process, both connected
to NATS, keeps the CA key out of the web tier's memory entirely, and the
signer can live on a separate machine with restricted network access. You do
not need NATS for a plain single-process deployment -- `ssoosshd serve` uses
an in-process transport by default. See
[Startup modes](/ssoossh/operations/startup-modes/).

## SQLite or PostgreSQL?

SQLite for a single instance (the default; put the file in
`/var/lib/ssoossh/` under systemd). PostgreSQL is required for multi-instance,
because a SQLite file cannot be shared safely between processes on different
hosts.
[Database](/ssoossh/operations/database/).

## What happens if NATS goes down, or an instance crashes?

Nothing is lost that matters: the flow is short and interactive, so the human
is the retry mechanism. A client waiting on a lost delivery keeps waiting,
then re-requests; the person who approved sees their terminal still waiting
and reruns the login. This is a deliberate at-most-once design -- JetStream is
not used. The failure modes one by one:
[Multi-instance and NATS](/ssoossh/operations/multi-instance/).

## Where are issued certificates stored?

Nowhere. The server never persists a signed certificate; delivery to the
waiting client is the only copy, so there is no certificate store to steal. A
client that misses the delivery window is answered `410 Gone` and simply
re-requests. What the database does keep is certificate *metadata* -- key ID,
serial, principals, fingerprint -- as the audit record of an issuance. See
[Database](/ssoossh/operations/database/).

## Where does the CA private key live?

In exactly one of four places: inline in `ssoosshd.yaml` as
[`ssh_key`](/ssoossh/reference/config/top-level/#ssh_key), in a file named by
[`ssh_key_file`](/ssoossh/reference/config/top-level/#ssh_key_file), in an
`ssh-agent` named by [`ssh_key_agent`](/ssoossh/operations/ssh-agent/), or in
a PKCS#11 token that it never leaves
([HSM and PKCS#11](/ssoossh/operations/hsm/)).
Either way, only a signing process loads it: run the signer as its own
`ssoosshd sign` process and the web tier holds no private key at all, learning
the CA *public* key from the signer's announcement instead. Cloud KMS signing
is not built; it would sit behind the same key-source interface.

## Can a compromised web tier, or a rogue admin, widen access?

No. The config file is the outer bound: nothing reachable over HTTP can make
issuance more permissive than the loaded configuration allows. Admin is an
OIDC group named in config, not a database flag, and an admin cannot approve
someone else's request, raise a ceiling, grant admin, or touch the audit
trail. A compromised web tier can deny service, not escalate.
[Roles and containment](/ssoossh/operations/roles/).

## How do I take someone's access away right now?

Disable them in the identity provider so they cannot authenticate, and disable
them in `ssoosshd` so they cannot sign in again -- that check, at login, is
fail-closed, and the disable requires a recorded reason. A web session already
open keeps working until it expires, so shorten that window if it matters (see
[the session lifetime](/ssoossh/operations/roles/#the-session-lifetime-is-the-revocation-window)). Certificates
they already hold expire on their own, which is why there is no revocation
list. Note that role membership is read at login, so the session lifetime
(default: 30m idle under a 9h absolute cap) is the window on a *role* change,
unless the directory is made authoritative for role groups
([`ldap.authoritative_role_groups`](/ssoossh/reference/config/ldap/#authoritative_role_groups)),
which takes a role away on the next request after the sync sees it go.
[Roles and containment](/ssoossh/operations/roles/).

## Can I lock down client settings across a fleet?

Yes: an `enforce` file, Windows Group Policy, or macOS managed preferences.
These are guardrails, not a security boundary -- the client runs as the user.
The only setting enforced beyond client cooperation is the server-side
`valid_duration` ceiling.
[Client settings enforcement](/ssoossh/hosts/client-enforcement/).

## The identity provider rejects the redirect URI

Almost always
[`http.public_url`](/ssoossh/reference/config/http/#public_url): it must be
the scheme and host browsers actually reach the deployment at, because the
OIDC redirect URI is derived from it. Compare what the provider has registered
against `<public_url>/auth/callback`.
[Identity provider](/ssoossh/operations/identity-provider/).

## Every request shows the proxy's IP in the audit trail

[`http.trusted_proxies`](/ssoossh/reference/config/http/#trusted_proxies) is
unset, so `X-Forwarded-For` is ignored. Set it to the proxy's CIDR. Until you
do, key IDs, console network gating, and lifetime source policy all see the
proxy rather than the client -- and source policy in particular then puts
everyone in the most generous tier.
[TLS and reverse proxies](/ssoossh/operations/tls-and-proxy/).

## ssoosshd refuses to start in api or sign mode

Both modes require [`pubsub.backend: nats`](/ssoossh/reference/config/pubsub/#backend);
they fail closed on the in-process backend because signing jobs could not
cross processes. The neighbouring startup failures are incomplete NATS mTLS
credentials, and `multi_instance: true` without an explicit
`http.cookie_key`. [Startup modes](/ssoossh/operations/startup-modes/).

## ssoosshd will not start and the message names a config key

That is by design: the whole configuration is validated at startup rather than
at first use. Templates are parsed and test-executed, CIDRs are parsed, mail
template overrides are rendered against an empty payload, and the CA key is
checked. A notification or a certificate that silently stops working is worse
than a process that refuses to start. Run `ssoosshd config check` to see
every problem at once, unknown keys and start-time warnings included, without
a restart per fix.
[Checking the config before a start](/ssoossh/operations/install/#checking-the-config-before-a-start).

## Why does the database refuse to start after a rollback?

Migrations are guarded against version skew: if the schema is newer than the
running build supports, the server stops rather than operating against a
schema it does not understand. `ALLOW_DOWNGRADE=true` in the environment lets
it go ahead, and it then migrates the schema **down** to the version the build
knows, running the newer migrations' down steps. Back up first.
[Database](/ssoossh/operations/database/).

## Do I have to configure email?

No. It is off by default and every certificate path behaves identically with
it disabled. When you do turn it on, nothing in a certificate flow ever waits
on the relay, and the enrollment code is in no message.
[Email notifications](/ssoossh/operations/email-notifications/).

## Does setting the LDAP block do anything yet?

Yes. Directory enrichment runs at login, the background sync refreshes
attributes and groups, and it can auto-disable a user who has left the
directory. [LDAP enrichment](/ssoossh/operations/ldap/).
