# Features

## The five things ssoossh solves

1. **No more key sprawl.** Servers stop holding `authorized_keys` files;
   they trust one CA. Add a server without copying keys to it; remove a
   person without touching a single machine.
2. **SSH access is your company login.** Getting in goes through the same
   identity provider as everything else, so joining, leaving, and group
   changes take effect where they are already managed. Leave the company,
   lose SSH.
3. **Credentials that expire on their own.** Certificates are short-lived
   by design. A key that leaked last week opens nothing today, and there
   is no revocation list to maintain; expiry does that work.
4. **You see what you grant, and it is all on record.** The approval page
   shows exactly what a certificate will allow, with anything policy
   trimmed struck through, before anyone approves. Every decision is
   recorded: who, from where, what was granted. `sshd` logs the key ID on
   every login, so the audit trail reaches the servers themselves.
5. **One flow for people, services, and sudo.** Interactive SSH,
   unattended service accounts, and `sudo`/`su` via PAM all go through
   the same CA, the same approval, the same policy.

What follows is what it does today, with status marked where a piece is
still landing. For setup, see [configuration.md](../operations/configuration.md) and
[deployment.md](../operations/deployment.md); what ssoossh deliberately does not do is
in [decisions.md](../project/decisions.md).

## Certificate types and status

| Type | Purpose | Status |
| --- | --- | --- |
| **User** | interactive SSH | shipped end to end |
| **PAM** | `sudo`/`su` via `pam_ssoossh` | shipped end to end |
| **Console** | interactive login at a machine with no browser, approved by a typed code | server and web UI shipped; the PAM module for it is being written separately in C |
| **Service** | non-interactive: enroll once, retrieve unattended, every retrieval logged | shipped end to end |

ssoosshd issues no host certificates: without a secure way for a host to
prove its claim to a hostname, host identity would be a hole rather than a
feature — see [decisions.md](../project/decisions.md). The client keeps
local principal-mapping tooling (`host mapping`, `host principals`) for
`AuthorizedPrincipalsCommand`; it has no server side.

## Issuance

- Every issuance is create, then human approval in the browser, then sign,
  then delivery to the still-waiting terminal. Denial resolves the client
  cleanly; requests expire on their own.
- **Requests ask, the server narrows, config gates.** The approval page
  shows what was asked for with anything policy trimmed struck through,
  *before* anyone approves. Nothing reachable over HTTP can exceed the
  config file.
- Issued certificates are never stored server-side. Delivery is the only
  copy, so there is no certificate store to steal.
- Lifetime derived from issuance context: per-group tiers and
  source-network rules, narrowing-only, longest-prefix wins
  ([certificate-lifetime-policy.md](../operations/certificate-lifetime-policy.md)).
- Key IDs, which are what `sshd` logs and therefore the audit trail, are
  shaped per type by a Go template
  (`{{.Username}}:{{.ClientIP}}:{{.UniqueID}}`...), including
  operator-defined extra OIDC claim fields captured at login
  (`{{.Extra.dept}}`); a bad template fails startup, not the first
  issuance, and a field with no value renders as `MISSING` rather than
  vanishing ([certificate-keyid-template.md](../operations/certificate-keyid-template.md)).
- Each type has its own template. An unset `service` template falls back to
  whatever `user` is configured with; `pam` deliberately does not, so a
  `sudo` and a login by the same person stay distinguishable in an audit
  log. The fields render from the *approver's* login, which for a service
  enrollment names the human who approved it — the key ID and principals
  are fixed at approval, because the approving identity is long gone by the
  time `service retrieve` redeems the code unattended.
- Every decision recorded append-only: who approved or denied, from where,
  when, and what was actually granted.

## Client

- Works from `ssh_config` (`ProxyCommand` / `Match exec`); keypair
  generated locally, private keys never leave the machine.
- Certificates load into your ssh-agent, or key files when no agent is
  available; valid certificates are reused until expiry, so one login can
  cover a workday. `logout` removes only ssoossh's material.
- macOS, Linux, and Windows, including Pageant and the WSL relay.
- Offline commands (`version`, `principals`) make no network call at all.
- Fleet-wide settings lockdown via an `enforce` file, Windows Group
  Policy, or macOS managed preferences
  ([client-settings-enforcement.md](../operations/client-settings-enforcement.md)).

## Server

- OIDC login; the identity provider stays authoritative for everything,
  including who is an admin.
- Web sessions with a sliding idle timeout (default 30m) under an absolute
  cap (default 9h) that activity never extends.
- CSRF, CSP with per-request nonces, HSTS, strict same-site cookies, and
  global plus per-endpoint rate limiting.
- SQLite or PostgreSQL; explicit migrations with a version-skew guard.
- Runs single-instance by default, or multi-instance behind a load
  balancer with NATS as the message broker (mTLS, per-node identity), with
  the signer optionally split into its own process so the CA key never
  shares memory with the web tier: `serve`, `serve api`, and `sign`
  startup modes ([deployment.md](../operations/deployment.md#6-startup-modes-full-api-and-sign)).
- The CA key comes from config or a PKCS#11 token (HSM or SoftHSM2); on
  the token path the private key never leaves the hardware
  ([hsm.md](../operations/hsm.md)), and in split mode the web tier holds no private key
  at all: signers announce their public keys to a registry and the web
  tier serves only those.
- Multiple CA keys can be active at once. Each signer announces its own
  key, `/api/ca` returns the full set, and clients and PAM trust a
  certificate signed by any of them, which covers key rotation and
  independent signers with distinct keys.
- The TLS certificate reloads without a restart, on `SIGHUP` and on an
  optional `http.tls.reload_interval` poll — the first is what
  `certbot --deploy-hook` and systemd `ExecReload=` reach for, the second
  covers a Kubernetes secret remount, which swaps a directory symlink
  where no signal or file watch would fire. A reload that fails logs at
  WARN and the certificate already serving keeps serving.
- OpenAPI spec and TypeScript types are generated from the code with CI
  drift checks. An unauthenticated `/api/version` reports build identity,
  which the site footer shows.

## In the browser

- Your own certificates, each traceable to what produced it: a service
  certificate links back to the code it was redeemed from and shows where
  it was fetched from.
- A **Service codes** view, by service account, that never shows a code: the
  accounts you hold, the enrollments approved for each, what a redemption
  grants, when it stops being redeemable, and how often anything has used
  it. Every holder of an account sees its codes, whoever approved them.
- Your own activity log.
- Notification preferences, per kind (see below).

## Admin and audit

- Config-driven **admin** and **auditor** roles, fail-closed: an empty
  group authorizes nobody, admins inherit the auditor views, and an admin
  can never approve someone else's request, raise a ceiling, grant admin,
  or touch the audit trail.
- **User directory** with a per-user detail page, and a disable/enable
  lifecycle. Disabling is fail-closed at login — a transient database error
  denies rather than admits — and it does exactly that and no more: service
  enrollments the person approved belong to their service accounts, not to
  them, so unattended jobs keep running and the account's other holders keep
  control. A disabled person lands on a page that can carry an operator-set
  message and contact address.
- **Certificate history across all users**, for "who issued this?": search
  over key ID, principals, serial, fingerprint, and owner, filtered by type
  and by live/expired, paged.
- **Service code directory** with a detail page and early expiry of an
  enrollment (idempotent).
- **Effective configuration** view for auditors: a fixed, deliberately
  chosen set of fields, never the whole file, and never the CA key, client
  secret, cookie signing key, or database password.

## Notifications

- Optional outbound email, off by default, telling people when something
  happens to a credential they hold: a service enrollment was created for
  one of their service accounts, and every redemption of it
  ([email-notifications.md](../operations/email-notifications.md)). An
  enrollment-scoped notification reaches every holder of the account, since
  that is who owns the code.
- Nothing in a certificate flow ever waits on the mail relay. An approval
  or a redemption publishes to the internal queue and returns; rendering
  and SMTP happen on a background consumer, so a relay that is slow,
  greylisting, or down delays only the notification.
- The enrollment code is in no message and must not be added to one.
- Each user picks which kinds they receive, read at delivery rather than at
  publication, so opting out still catches something already queued. In a
  fan-out each holder's own choice gates their own copy.
  Templates can be overridden, and a bad override fails startup.

## PAM

- `pam_ssoossh` puts `sudo` and `su` behind the identity provider: a
  per-attempt ephemeral keypair, a short-lived certificate, four checks
  (CA signature, key binding, principals, validity window), and nothing
  retained on disk. Fail-closed everywhere
  ([pam.d-sudo.example](../pam.d-sudo.example)).
- The certificate names the **approver**: their OIDC username and the other
  accounts they hold. It does not name the local account the module is
  authenticating, which an unauthenticated client reports and which is used
  for display and audit only. Which of the approver's names may assume which
  local account is the host's decision, made by check 3 against the local
  `principals-map`, so `sudo` on a machine is authorized by a file only root
  on that machine can edit
  ([design](../proposals/pam-principal-source.md)).

## Console login

A console has a human in front of it and no browser: a physical tty, a
serial console, a BMC or KVM viewer, a VM console. There is nothing on that
screen to copy, so printing an approval URL nobody can transcribe is not a
flow.

- The machine calls `POST /api/certs/console` and is answered with an
  eight-character code (Crockford Base32, shown as `K7M4-QP2X`), the page
  that accepts it, a `/c/<code>` shortcut for a phone, and `expires_at` —
  the deadline the server will hold the request to.
- The approver signs in and types the code at **/console**, or opens the
  shortcut. Resolving a code needs a session: an unauthenticated caller can
  never turn a code into a request ID, and the request ID is what the
  certificate is delivered against. Resolving also claims the request, so a
  second person typing the same code is refused.
- **The code is the consent-phishing control**, not just a convenience.
  Request binding stops one user approving another's pending request; it
  does nothing about a user talked into approving a request an attacker
  created for them. A code that exists only on the console screen raises
  that from "click this link" to "read me the eight characters in front of
  you".
- The approval page shows what is being approved: the machine, the PAM
  service, the terminal, the account being logged into, and the address the
  server observed. Everything but that last one is self-reported by an
  unauthenticated caller and is labelled as such. A console login that also
  reports a remote host is flagged outright — a console has nobody
  connecting to it over the network.
- Timing is its own budget. `cert_options.console.client_timeout` defaults
  to 2m against the global 5m ceiling, because the approval window is the
  attacker's working time in the phone-call case above. A type may only
  shorten the global budget, never extend it.
- `cert_options.console.allowed_networks` refuses a request from outside
  named networks at creation, before a keypair is certified and before any
  human is asked. It gates on the source address the server observes rather
  than a hostname the caller typed, for the same reason host certificates
  were declined.
- **Which accounts may console into which machine is the host's decision,
  not the server's.** Put `pam_succeed_if.so user ingroup ...` above the
  ssoossh line in `/etc/pam.d/login`: host-side, root-owned, and it fails
  before any network call. A group sent on the wire would be untrusted
  input that stops applying the moment someone omits it.
- As with `sudo`, the certificate names the **approver**, and the host's
  `principals-map` decides which local account that authorizes. Someone who
  types `root` at an unattended console gets a certificate their host's map
  refuses unless it already says that approver may become root.

The module that drives this from the console side is written and shipped
separately. Server-side design and reasoning:
[console-login-pam.md](../proposals/console-login-pam.md).

## Coming later

None of the following is built. Where a design exists it is linked, and
each design states its own status and the commit its `file:line` anchors
were verified against — see [proposals/](../proposals/).

- **Certificate lifetime policy rework** — untangling source-address
  pinning from the lifetime rule, which today welds two unrelated policy
  questions onto one list
  ([design](../proposals/certificate-lifetime-policy-rework.md)).
- **Source-address restrictions** — approver-chosen pinning and a
  retrieval allowlist, superseding `pin_source_address`
  ([design](../proposals/source-address-restrictions.md)).
- **Claim-driven certificate policy** — driving lifetime, extensions, and
  type gating from numeric OIDC claims
  ([design](../proposals/claim-driven-certificate-policy.md)).
- **Service retrieval anomaly policy** — alerting on, and locking, an
  enrollment code redeemed from too many source networks
  ([design](../proposals/service-retrieval-anomaly-policy.md)).
- **LDAP enrichment** — additional principals and account identifiers from
  a directory. The config schema is parsed today but nothing consumes it,
  so setting it changes nothing yet.
- **Config coordination** — detecting and reporting configuration
  divergence between instances sharing a database and a NATS cluster
  ([design](../proposals/config-coordination.md)).
- **Cloud KMS signing**, behind the same key-source interface the config
  and PKCS#11 backends use today.
- **QR-code approval at the console**, so the verification URL can be
  photographed instead of typed. The server already returns the short
  `/c/<code>` URL a QR has to encode; drawing it is the console module's
  half ([design](../proposals/console-login-pam.md)).
- **Push approval to a registered device**, deferred rather than rejected:
  request creation is unauthenticated, so an opt-in, per-target-user rate
  limit has to come first
  ([design](../proposals/console-login-pam.md)).
- **Host certificates**, only if a secure host-verification mechanism
  (something like an ACME challenge) makes hostname claims provable —
  see [decisions.md](../project/decisions.md).
