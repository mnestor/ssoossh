# Features

## The five things ssoossh solves

1. **No more key sprawl.** Servers stop holding `authorized_keys` files —
   they trust one CA. Add a server without copying keys to it; remove a
   person without touching a single machine.
2. **SSH access is your company login.** Getting in goes through the same
   identity provider as everything else, so joining, leaving, and group
   changes take effect where they are already managed. Leave the company,
   lose SSH.
3. **Credentials that expire on their own.** Certificates are short-lived
   by design. A key that leaked last week opens nothing today, and there
   is no revocation list to maintain — expiry does that work.
4. **You see what you grant, and it is all on record.** The approval page
   shows exactly what a certificate will allow — with anything policy
   trimmed struck through — before anyone approves, and every decision is
   recorded: who, from where, what was granted. `sshd` logs the key ID on
   every login, so the audit trail reaches the servers themselves.
5. **One flow for people, servers, services, and sudo.** Interactive SSH,
   host identity, unattended service accounts, and `sudo`/`su` via PAM
   all go through the same CA, the same approval, the same policy.

What follows is everything it does today. For setup, see
[deployment.md](deployment.md) and the annotated config samples; what it
deliberately does not do is in [decisions.md](decisions.md).

## Issuance

- Three certificate types: **user** (interactive SSH), **host** (server
  identity — `host sign` first, `host renew` after, authenticated by the
  existing certificate), and **service** (non-interactive: enroll, then
  retrieve).
- Every issuance is create → human approval in the browser → sign →
  delivery to the still-waiting terminal. Denial resolves the client
  cleanly; requests expire on their own.
- **Requests ask, the server narrows, config gates.** The approval page
  shows what was asked for with anything policy trimmed struck through,
  *before* anyone approves. Nothing reachable over HTTP can exceed the
  config file.
- Issued certificates are never stored server-side — delivery is the only
  copy, so there is no certificate store to steal.
- Lifetime derived from issuance context: per-group tiers and
  source-network rules, narrowing-only, longest-prefix wins
  ([certificate-lifetime-policy.md](certificate-lifetime-policy.md)).
- Key IDs — what `sshd` logs, so the audit trail — shaped per type by a
  Go template (`{{.Username}}:{{.ClientIP}}:{{.UniqueID}}`…); a bad
  template fails startup, not the first issuance.
- Every decision recorded append-only: who approved or denied, from where,
  when, and what was actually granted.

## Client

- Works from `ssh_config` (`ProxyCommand` / `Match exec`); keypair
  generated locally, private keys never leave the machine.
- Certificates load into your ssh-agent, or key files when no agent is
  available; valid certificates are reused until expiry, so one login can
  cover a workday. `logout` removes only ssoossh's material.
- macOS, Linux, and Windows, including Pageant and the WSL relay.
- Host enrollment, per-host principal mapping for
  `AuthorizedPrincipalsCommand`, and service-account certificates for
  unattended jobs.
- Offline commands (`version`, `principals`) make no network call at all.
- Fleet-wide settings lockdown via an `enforce` file, Windows Group
  Policy, or macOS managed preferences
  ([client-settings-enforcement.md](client-settings-enforcement.md)).

## Server

- OIDC login; the identity provider stays authoritative for everything —
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
  shares memory with the web tier — `serve`, `api`, and `sign` startup
  modes ([deployment.md](deployment.md)).
- The CA key lives in an ssh-agent, never in process memory; OpenAPI spec
  and TypeScript types are generated from the code with CI drift checks.

## PAM

- `pam_ssoossh` puts `sudo` and `su` behind the identity provider: a
  per-attempt ephemeral keypair, a short-lived certificate, four checks
  (CA signature, key binding, principals, validity window), and nothing
  retained on disk. Fail-closed everywhere
  ([pam.d-sudo.example](pam.d-sudo.example)).

## Coming later

- **LDAP enrichment** — additional principals and account identifiers
  from a directory, feeding user disablement sweeps.
- **Admin user-disable flow** — disable a departed user with a grace
  period and a preview of which enrollments and unattended jobs it will
  break.
- **Approver identity in key IDs** — service-certificate key IDs naming
  the human who approved them.
- **HSM / PKCS#11 / cloud KMS signing** — behind the same signing
  interface the ssh-agent uses today.
- **Runtime-editable narrowing policy** — admins tightening (never
  loosening) policy from the web UI, fully audited.
