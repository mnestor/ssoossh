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
still landing. For setup, see [configuration.md](configuration.md) and
[deployment.md](deployment.md); what ssoossh deliberately does not do is
in [decisions.md](decisions.md).

## Certificate types and status

| Type | Purpose | Status |
| --- | --- | --- |
| **User** | interactive SSH | shipped end to end |
| **PAM** | `sudo`/`su` via `pam_ssoossh` | shipped end to end |
| **Service** | non-interactive: enroll once, retrieve unattended, every retrieval logged | shipped end to end |

Host certificates were removed rather than finished: without a secure way
for a host to prove its claim to a hostname, issuing host identity is a
hole, not a feature — see [decisions.md](decisions.md). The client keeps
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
  ([certificate-lifetime-policy.md](certificate-lifetime-policy.md)).
- Key IDs, which are what `sshd` logs and therefore the audit trail, are
  shaped per type by a Go template
  (`{{.Username}}:{{.ClientIP}}:{{.UniqueID}}`...), including
  operator-defined extra OIDC claim fields captured at login
  (`{{.Extra.dept}}`); a bad template fails startup, not the first
  issuance, and a field with no value renders as `MISSING` rather than
  vanishing ([certificate-keyid-template.md](certificate-keyid-template.md)).
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
  ([client-settings-enforcement.md](client-settings-enforcement.md)).

## Server

- OIDC login; the identity provider stays authoritative for everything,
  including who is an admin.
- Config-driven **admin** and **auditor** roles, fail-closed: an empty
  group authorizes nobody, admins inherit the auditor views, and an admin
  can never approve someone else's request, raise a ceiling, grant admin,
  or touch the audit trail.
- Web sessions with a sliding idle timeout (default 30m) under an absolute
  cap (default 9h) that activity never extends.
- CSRF, CSP with per-request nonces, HSTS, strict same-site cookies, and
  global plus per-endpoint rate limiting.
- SQLite or PostgreSQL; explicit migrations with a version-skew guard.
- Runs single-instance by default, or multi-instance behind a load
  balancer with NATS as the message broker (mTLS, per-node identity), with
  the signer optionally split into its own process so the CA key never
  shares memory with the web tier: `serve`, `serve api`, and `sign`
  startup modes ([deployment.md](deployment.md#6-startup-modes-full-api-and-sign)).
- The CA key comes from config or a PKCS#11 token (HSM or SoftHSM2); on
  the token path the private key never leaves the hardware
  ([hsm.md](hsm.md)), and in split mode the web tier holds no private key
  at all: signers announce their public keys to a registry and the web
  tier serves only those.
- Multiple CA keys can be active at once. Each signer announces its own
  key, `/api/ca` returns the full set, and clients and PAM trust a
  certificate signed by any of them, which covers key rotation and
  independent signers with distinct keys.
- OpenAPI spec and TypeScript types are generated from the code with CI
  drift checks.

## PAM

- `pam_ssoossh` puts `sudo` and `su` behind the identity provider: a
  per-attempt ephemeral keypair, a short-lived certificate, four checks
  (CA signature, key binding, principals, validity window), and nothing
  retained on disk. Fail-closed everywhere
  ([pam.d-sudo.example](pam.d-sudo.example)).

## Coming later

- **ACME for TLS**, setup acme for public trusted ssl certs on webserver
- **LDAP enrichment**: additional principals and account identifiers from
  a directory, feeding user disablement sweeps.
- **Admin user-disable flow**: disable a departed user with a grace period
  and a preview of which enrollments and unattended jobs it will break.
- **Approver identity in key IDs**: service-certificate key IDs naming the
  human who approved them.
- **Cloud KMS signing**, behind the same key-source interface the config
  and PKCS#11 backends use today.
- **Runtime-editable narrowing policy**: admins tightening (never
  loosening) policy from the web UI, fully audited.
- **Host certificates**, only if a secure host-verification mechanism
  (something like an ACME challenge) makes hostname claims provable —
  see [decisions.md](decisions.md).
