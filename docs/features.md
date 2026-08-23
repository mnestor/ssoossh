# Features

What ssoossh does today, one section per feature, with the invariants each
one is built around. For the problem it solves and the concepts, read
[what-ssoossh-is.md](what-ssoossh-is.md) first; for how to set any of this
up, [deployment.md](deployment.md) and the annotated config samples.

## The components

**ssoossh client** — turns an OIDC login into a short-lived SSH
certificate, from your `ssh_config`. Configured as a `ProxyCommand` or
`Match exec`, it generates a fresh keypair, hands the public key to the
server, opens your browser for OIDC authentication, and loads the signed
certificate into your ssh-agent (or writes key and certificate files when
no agent is available). Private keys never leave the machine. Valid
certificates are reused until they expire, so authenticating once can cover
a workday. Runs on macOS, Linux, and Windows; also handles host enrollment,
per-host principal mapping for `AuthorizedPrincipalsCommand`, and
service-account certificates for unattended jobs.

**ssoossh server (`ssoosshd`)** — the trust anchor and policy decision
point. Authenticates users through OIDC, signs SSH public keys into
certificates carrying the principals, critical options, extensions, and
validity window that policy allows, and serves the web UI for approval and
per-user certificate history. Never receives a private key. The server
configuration is the outer bound on everything it issues.

**pam_ssoossh** — a Linux PAM module for the auth management group, scoped
to `sudo` and `su`. Per attempt it generates an ephemeral keypair, requests
a certificate, and verifies the CA signature, the key binding, the
principals, and the validity window. Nothing is retained — no key, no
certificate, nothing on disk. See [pam.d-sudo.example](pam.d-sudo.example).

## Certificate issuance

Three certificate types: **user** (interactive SSH), **host** (server
identity: `host sign` first, then `host renew` authenticated by the
existing certificate), and **service** (non-interactive, enrolled then
retrieved). Every issuance follows create → human approval in the browser →
sign → delivery over SSE to the still-waiting client.

The load-bearing invariant: **requests ask, the server narrows, config
gates.** A client may request any options; the server intersects them with
what configuration permits and the approval page shows the result — what
was asked for, with anything policy trimmed struck through — *before*
anyone approves. Nothing reachable over HTTP can make issuance more
permissive than the loaded config file.

Requests expire (`cert_options.request_ttl`, default 5m, must be positive)
and denial resolves the waiting client cleanly with no certificate. Issued
certificates are never persisted server-side — delivery is the only copy,
and a client that misses it re-requests.

## Lifetime and approval policy

Certificate lifetime is derived from issuance context, not one flat
number: `server/service/lifetimepolicy.go` evaluates group tiers and
source-network rules against a per-type ceiling. The full semantics,
config shapes, and operator footguns are in
[certificate-lifetime-policy.md](certificate-lifetime-policy.md); the
short version:

- **Narrowing only.** A rule can shorten a lifetime, drop extensions, or
  (service certificates only) add restricting critical options such as
  `source-address`. No rule can exceed the configured ceiling.
- **Longest-prefix wins** for source rules, routing-table style; equal
  prefixes resolve to the stricter rule, so a duplicate can never loosen
  anything. IPv4-mapped IPv6 addresses are unmapped before matching.
- **No match means no reduction** — the type's ceiling applies. An
  operator who wants a floor writes an explicit `0.0.0.0/0` / `::/0` pair,
  firewall-style.
- The address consulted is the request's server-observed source
  (`g.ClientIP()` through `http.trusted_proxies`), never the
  client-supplied interface list and never the approver's browser address.
- User certificates are never pinned to a source address — people move
  networks; services sit still.
- Groups feed decisions (lifetime tiers, authorization) and **never appear
  in certificate content**.

## Key ID templating

The certificate key ID — what `sshd` logs on every authentication, so the
audit trail — is shaped per type by a Go `text/template`:

```yaml
cert_options:
  user:
    key_id_template: "{{.Username}}:{{.ClientIP}}:{{.UniqueID}}"
```

Fields: `{{.Username}}`, `{{.Subject}}`, `{{.Email}}` (user/service),
`{{.ClientIP}}` (all), `{{.Hostname}}` (host), `{{.UniqueID}}` (all — the
request ID, guaranteeing uniqueness when everything else collides).
Service and host templates fall back to the user template when unset;
with nothing configured the key ID is `{{.Username}}` (host:
`{{.Hostname}}`). Templates parse at startup, so a typo is a config error,
not a broken first issuance. Unknown fields fail startup by design — the
field set will grow (approver identity fields are designed but not built).

## Admin and auditor roles

Three config-driven roles, each an OIDC group named in configuration —
never a database flag, so there is no bootstrapping problem, the identity
provider stays authoritative, and an admin cannot grant admin:

```yaml
admin:
  require_group: "SSH Admins"          # empty disables admin surfaces
  auditor_group: "SSH Auditors"        # read-only; admins inherit it
  ssh_server_admin_group: "SSH Hosts"  # host-certificate administration
```

- **admin** — expiring enrollments, disabling users. **auditor** — the
  read-only views: cross-user certificate history, effective configuration
  (secrets redacted). Auditor is a child role: every admin holds auditor
  access; an empty `auditor_group` narrows those routes to admins rather
  than opening them.
- **Everything fails closed.** No identity, no configured group, or no
  membership all deny; an empty group never authorizes anyone.
- What an admin can never do, by construction: approve someone else's
  request (the certificate takes the approver's principals — that would be
  an escalation channel), raise any configured ceiling, grant admin, reach
  key material, or touch the audit trail.
- Roles are evaluated from the session, not the database — groups are
  deliberately not persisted, so removing someone in the IdP takes effect
  at their next login, and the session's absolute cap (below) bounds the
  revocation window.

## Web sessions

Two-tier lifetime, the same shape as a corporate SSO:
`http.cookie_idle_timeout` (default 30m) slides on activity, so an
abandoned browser expires after one quiet window while active use
continues; `http.cookie_max_age` (default 9h) is the absolute cap measured
from login that activity never extends — and therefore also the ceiling on
group-claim staleness. There is deliberately no client-side keepalive: a
poll would keep an unattended browser alive, which is the case the idle
window exists for. CSRF protection, strict same-site cookies, security
headers, and global plus per-endpoint rate limiting sit in front of all of
it.

## Client settings enforcement

Client settings can be locked fleet-wide, per platform: an `enforce` YAML
file named by the machine-wide config, Windows Group Policy (registry), or
macOS managed preferences (plist). All of it is a guardrail rather than a
security boundary — the client runs as the user — and the full precedence
chain, the per-platform mechanics, and the caveats live in
[client-settings-enforcement.md](client-settings-enforcement.md).

## Offline commands

Commands that need nothing from the server (`version`, `principals`)
declare themselves offline and skip API client setup entirely: they make
no network call, and receive a refusing client rather than a nil one, so a
missed call path errors clearly instead of panicking.

## Wire contract and generated artifacts

The HTTP API is described by [openapi.yaml](openapi.yaml), generated from
handler annotations (`make openapi`); TypeScript types for the web UI are
generated from the Go wire types by tygo (`make types`); both have CI
drift checks. See [wire-types.md](wire-types.md).

## Multi-instance

Supported with NATS as the pub/sub backend; single-instance (the default,
in-process transport) otherwise. See [deployment.md](deployment.md) and
[multi-instance-safety-plan.md](multi-instance-safety-plan.md) for status
and what remains.
