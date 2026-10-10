# NSS directory: uid/gid lookups for hosts

**Status:** proposal. Decisions taken 2026-10-09 and 2026-10-10 (see
[Decisions](#decisions)). Nothing built.

**Anchors verified at:** `19e2c09` (2026-10-09). `file:line` references drift;
re-check before relying on one. The companion repository `mnestor/ssoossh-pam`
was read at its `main` on the same day.

## What this proposes

A host resolves a username to its uid, primary gid, all group memberships,
home and shell from ssoosshd over HTTPS, with nothing provisioned per host.
Ephemeral hosts boot from an image and have a working directory; long-lived
hosts keep a persistent cache and stop beating on the server.

It is the NSS counterpart to `pam_ssoossh`: PAM answers "may this person
act", NSS answers "what POSIX identity is this name". Login authorization
stays with SSH certificates. Nothing here issues or validates one.

Two repositories, one wire contract:

| Piece | Where | Why there |
| --- | --- | --- |
| `/api/nss/*`, a `user_posix` table, LDAP field destinations, `admin.nss_key_group`, API-key admin UI | `mnestor/ssoossh` | It is the identity source. OIDC, LDAP, admin roles, audit and the web UI are already here; a second server would mean a second user store. |
| `ssoosshd-nss` daemon, `libnss_ssoossh.so`, nfpm packages, `ssoossh-nss-selinux` | `mnestor/ssoossh-nss` (new) | Host software with its own toolchain, release cadence and failure mode, as `ssoossh-pam` is kept separate. |
| JSON response shapes, endpoint paths | `docs/wire-contract.json` | The existing cross-repo contract; bumped on any wire change. |

Server side it is behind `nss.enabled` and defaults off. The host package
never needs the certificate pipeline.

## What exists today, and what this assumes

| Assumption | Reality in the repos | Effect |
| --- | --- | --- |
| No host daemon exists | Correct. `ssoossh host` has only `principals` and `mapping`, both offline; "synced from the server" in `client/cmd/host.go:12` is stale help text. `deploy/ssoossh-agent.service` is the CA's ssh-agent, not a host agent. | Daemon is new. |
| The user model carries POSIX attributes | **Wrong.** LDAP enrichment (`server/service/ldap.go:416`) maps attributes into `other_accounts`, `service_accounts`, `groups`, `name` and untyped extras. No `uidNumber`, `gidNumber`, `homeDirectory` or `loginShell` anywhere. | New `user_posix` table, migration pair, new `ldap.fields` destinations, `make confdocs`. |
| Hosts authenticate to the server | **Wrong.** Clients send no credential; context is "a claim, not a control" (`internal/apitypes/certrequest.go`). | Fleet API keys, below. |
| PAM talks HTTPS from inside the module | Correct: libcurl, TLS 1.3 only, OS trust store, no pinning, proxy env deliberately left on (`src/httpc.c`). Console flow with code and QR exists; QR is rendered in-module by vendored `qrcodegen`. | PAM stays standalone; two small hardening PRs, below. |

## Threat model

The asset is the name→uid mapping. An attacker who can make a host believe
`alice → uid 0`, or `alice → bob's uid`, owns the host. In priority order:

1. Code loaded into setuid processes (`sudo`, `su`, `passwd`, `sshd`). Any
   TLS, JSON or environment-reading code in the `.so` is attack surface in
   those processes.
2. A lying or compromised endpoint: edge proxy, load balancer, app tier. TLS
   protects the wire, not the server.
3. Replay of a captured response (mitigated by TLS alone, as with sssd).
4. Availability: a slow or dead directory wedging logins, `sudo`, `ls -l`,
   and boot.

Every lookup requires a fleet API key, so exposure is that of LDAP with a
shared bind account (the usual sssd deployment), not anonymous bind. Any host
holding a key can ask for any name; that is accepted. Out of scope: per-host
identity (mTLS, host certificates). The key is baked into the image, never
provisioned per host.

## Architecture

Split by process, not by language, as sssd does (`libnss_sss.so` + `sssd`):

- `libnss_ssoossh.so` (C, no runtime) is loaded into every caller, including
  setuid binaries. It writes a name to a Unix socket, reads back a fixed
  struct, fills the caller's buffer, returns. No TLS, no JSON, no env reads,
  no network.
- `ssoosshd-nss` (Go, root) does everything else: pinned HTTPS with the
  fleet key, the cache, host-side options. One process to audit.

Go cannot be the `.so`: a cgo-exported shared object drags the runtime (GC,
scheduler, signal handlers) into every host process, and the Go runtime is
not `fork()`-safe, which NSS callers rely on constantly. Go is right for the
daemon: an ordinary long-lived process; stdlib HTTPS and JSON, one goroutine
per socket connection, a background refresh ticker; shared config loading
with the existing client binary.

## Wire format and transport trust

Responses are plain JSON over HTTPS, as sssd's LDAP responses are plain
entries over TLS. No response signing: every peer (sssd, nslcd, OS Login,
hosted directories such as JumpCloud and Okta LDAP) trusts TLS plus a
credential, and the residual risk of a lying intermediary at the edge is the
operator's infrastructure to secure, not the directory's. An earlier draft
carried a JWS/JWKS layer with a root pin and holder binding; it was dropped
on 2026-10-10 as complexity out of proportion to the exposure.

**Transport trust, sssd-shaped.** The daemon's one trust root is its TLS
pin, configured like `ldap_tls_cacert` / `ldap_tls_reqcert = demand`:
`tls_ca_file` (a private CA bundle) or `tls_spki_pin` (SHA-256 of the
server's SubjectPublicKeyInfo), baked into the image. The system bundle is
not used; proxy and certificate environment variables are ignored. TLS 1.3
only, matching `ssoossh-pam`.

**User response** (`/api/nss/user/name/:name` or `/uid/:uid`, inside the standard
`{data, error, error_code}` envelope):

```json
{
  "name": "mike",
  "uid": 1234,
  "gid": 1234,
  "groups": [{"gid": 1234, "name": "mike"}, {"gid": 27, "name": "sudo"}],
  "home": "/home/mike",
  "shell": "/bin/bash",
  "shell_pref": "zsh",
  "gecos": "Mike Nestor",
  "status": "active",
  "key_id": "nss-team-a"
}
```

`groups` carries every membership, primary first, gid and name together, so
one call fills `initgroups`, `getgrgid` and `getgrnam`; the daemon writes
each entry into its group cache keyed by both. A group response
(`/api/nss/group/name/:name` or `/gid/:gid`) is `{name, gid, members}`. Both
shell fields are sent: `shell` is the LDAP `loginShell` path or server
default (null if neither), `shell_pref` the user's chosen shell as a name
(null if unset). The host tries `shell_pref` first and falls back to `shell`,
so a preference the host cannot honour degrades to the directory value
rather than a generic fallback (Part A). `key_id` names the
fleet key that made the request, for log correlation.

The JSON shapes are versioned in `docs/wire-contract.json`, the mechanism
that keeps `ssoossh-pam` in step. `/api/ca` is unchanged.

## Part A: server changes (`mnestor/ssoossh`)

### Endpoints

All inside the standard `{data, error, error_code}` envelope, with swag
annotations, a `docs/wire-contract.json` bump, and the existing per-IP
limiter from the cert-request routes.

| Path | Method | Auth | Returns |
| --- | --- | --- | --- |
| `/api/nss/user/name/:name` | GET | fleet key; limits per (key, source IP) | user JSON (uid, gid, groups, home, shell, status); 404 with `error_code` |
| `/api/nss/user/uid/:uid` | GET | fleet key | same |
| `/api/nss/group/name/:name` | GET | fleet key | `{name, gid, members}`; needed only for groups no cached user belongs to |
| `/api/nss/group/gid/:gid` | GET | fleet key | same |

Path parameters rather than query strings, matching the existing
`/api/certs/requests/:id` style. `:name` is validated against the same
conservative pattern the daemon enforces; `:uid` and `:gid` must be unsigned
integers.

No separate `initgroups` endpoint: the user response carries the full group
list.

### Data: new, not reused

A `user_posix` table (uid, gid, home, shell, `shell_pref`, status, timestamps) joined to
`users`; `users` is unchanged. A timestamped migration pair in both the
sqlite and postgres trees (single-owner directories per `AGENTS.md`). New
reserved `ldap.fields` destinations: `uid_number`, `gid_number`,
`home_directory`, `login_shell`, `posix_groups`, resolved in
`server/service/ldap.go` beside the existing ones. `make confdocs`
regenerates the reference.

### Directory sync and account status

NSS must resolve users who have never logged in.

- **On demand.** An unknown name or uid on the user routes triggers the same
  LDAP lookup and upsert that first login does, then serves from the DB.
  Server-side negative cache 60 s. LDAP unreachable: serve from the DB.
- **Background.** A scheduler job (the scheduler and an admin `ldap/sync`
  endpoint already exist) re-reads every user and group with
  `uidNumber`/`gidNumber` every 15 min (`modifyTimestamp` where supported),
  full refresh nightly. It updates fields, membership and status; it never
  deletes rows.
- **No gate.** Every user with POSIX attributes is resolvable. ssoossh is the
  directory, not a gatekeeper; narrowing belongs on the host
  (`require_groups`, `filter_users`). Users without `uidNumber` are 404 by
  construction (server-side allocation is a later option).
- **Status, not absence.** A user the server has ever known is never a 404
  again, only a status change:

| Status | passwd entry | Effect |
| --- | --- | --- |
| `active` | normal | |
| `disabled` | name, uid, gid, gecos, home kept; shell → `disabled_shell` (`/usr/sbin/nologin`); passwd → `!` | `ls -l`, `id`, file ownership keep working; interactive login fails even if something bypasses the cert gate (issuance is already refused for disabled users) |
| `deleted` | as `disabled` for `tombstone_retention` (90 d), then 404 | uid→name survives for audits and cleanup |

Group memberships are kept through `disabled`. A host that sees a login
resolve to a `disabled` entry logs at warning.

**User self-service: shell only.** The existing profile page
(`/api/users/me`) gains one field, the `chsh` equivalent: a user picks a
login shell. Home directory is not user-settable. The preference is stored
and served as a **name**, not a path (`shell_pref: "zsh"`), because the same
shell lives at different paths on different hosts (`/bin/bash` vs
`/usr/local/bin/bash`, `/usr/bin/zsh` vs `/bin/zsh`). The server validates
against `nss.allowed_shells` (an operator list of names, `[sh, bash, zsh,
fish]`; the UI shows a dropdown) and audit-logs changes.

**Host-side resolution.** The response carries both `shell` (the LDAP
`loginShell` path or server default) and `shell_pref` (the user's chosen
name). The daemon resolves `shell_pref` against `/etc/shells` by basename,
first match wins, so each host maps `zsh` to wherever its zsh is; an
optional `shell_map` in daemon config (`zsh: /opt/homebrew/bin/zsh`)
overrides the lookup for odd installs. If the name has no match, or the
resolved path is not in the host's `allowed_shells` or is in
`vetoed_shells`, the daemon falls back to `shell`, checked the same way;
only if that also fails does `shell_fallback` apply. A rejected preference
degrades to the directory's shell, never fails the lookup. Precedence: host
`override_shell` → `shell_pref` resolved on the host → `shell` from LDAP or
server default → host `shell_fallback`.

**Fields served.** passwd: name (`uid`/`sAMAccountName`), passwd (`*`, never
`x`; `!` when disabled), uid (`uidNumber`, required), gid (`gidNumber`, else
`auto_private_groups`, overridable on the host by `override_gid`), gecos
(`displayName`/`cn`), home (`homeDirectory`, then host override/fallback),
shell (`shell_pref` as a name resolved on the host via `/etc/shells` or
`shell_map`, else `shell` from `loginShell` or server default, each checked
against the host's allow/veto lists, then host override/fallback). group:
name/gid (`cn`/`gidNumber` on `posixGroup`, synthesized for private groups),
members (`memberUid` or resolved `member` DNs, capped). shadow, netgroups,
gshadow: not served. Home-directory creation is a host concern
(`pam_mkhomedir`, `pam_systemd_home`).

`nss.auto_private_groups` (`hybrid` default) is evaluated on the server
against the full group table so every host receives the same gid and
collisions are caught centrally.

### Fleet API keys

Any valid key resolves any user. Keys are a fleet credential baked into a
team's image; several can be active at once (rotation overlap, a key per
team); each is individually revocable with a usage log, unlike a shared LDAP
bind password.

| Field | Notes |
| --- | --- |
| `id` | random slug generated at creation, never changes for the life of the key; sent as its own header; appears in logs |
| `current_generation` | integer, 1 at creation, incremented on every rotation; usage rows reference it instead of any secret material |
| `secret_hash` | Argon2id or SHA-256 of the current secret; plaintext shown once, never stored |
| `previous_secret_hash`, `previous_expires_at` | the secret of `current_generation - 1`, accepted until its grace expiry (default 30 days) so images carrying the old secret keep working while the fleet rolls; clearable early |
| `name`, `description`, `created_by` | team or fleet name, optional free-text description, creating subject |
| `group` | optional OIDC/LDAP group the key is assigned to; its members may view the key, read its usage log, rotate and revoke it, so a team manages its own key without holding `nss_key_group` |
| `expires_at` | required; default one year; UI warns 30 days out |
| `notification_email` | where expiry, rotation and dead-key notices go; defaults to the creator's address, a team list is the intended value |
| `revoked_at`, `revoked_by` | immediate kill of the key and both secrets |

Last-used time, address and generation are not stored on the key; they are
derived from the usage table below.

Two headers, in the shape of Cloudflare Access service tokens:
`X-Ssoossh-Key-Id: <id>` and `X-Ssoossh-Key-Secret: <secret>`. The server
looks up by id, compares the hash in constant time, then serves the lookup;
unknown id and bad secret both return 401 with the same body and timing. The
secret is 32 random bytes, base64url, prefixed `ssoossh_nss_` for secret
scanners; the id is a short random slug (`nss_k7m4qp2x`). A single joined
bearer string was rejected: separate headers keep the id loggable and the
secret redactable at the proxy. Every lookup is logged with key id, name,
hit/miss, source address.

**Rate limiting.** A fleet key is shared by every host built from its image,
so a per-key hard limit would throttle the whole fleet at once. The limiter
buckets on `(key_id, source IP)`: 20 req/s, burst 200, above anything
coalescing plus stale-while-revalidate produces and bounded by the daemon's
`refresh_rate`. The IP is the connection's, never a header, so a client
cannot mint buckets. A plain per-IP limit stays as the backstop for
unauthenticated traffic. Per-key miss rate is an alert in the key's usage
log (the enumeration signal), not a block, so a fleet-wide boot storm after a
bad image cannot lock every host out at once. Hosts behind one NAT share a
bucket; size the limit or front them with a proxy that preserves source
addresses.

**Role.** A new config-declared role beside `admin.require_group`,
`admin.soc_group`, `admin.auditor_group`: `admin.nss_key_group`, following the
`AdminConfig` doc-comment + `mapstructure` + `default` convention
(`server/config/types.go:371`), fail-closed like the others.

- `nss_key_group`: create keys, set or change a key's `group` and
  `description`, list all, rotate (new secret under the same id; the old
  secret moves to previous with a grace expiry), revoke, read all usage logs.
- Members of a key's `group`: view that key, read its usage log, rotate and
  revoke it; not create keys or change a key's `group`.
- `require_group` (admin): everything, as admins inherit every narrower role.
- `soc_group`: revoke and read logs for all keys (containment), not create
  or rotate.
- `auditor_group`: read all metadata and logs.

Rotation runbook: rotate the key (id stays, new secret issued, old secret
valid for the grace window), rebuild the image with the new secret, roll the
fleet, watch the rotation report's not-switched count reach zero, then clear the previous
secret early or let the grace lapse. Only the secret ever changes; configs,
logs and alerts keyed on the id are untouched. Annual expiry is a floor.

**Web UI.** A keys page in the admin area, filtered by role: `nss_key_group`,
admins, SOC and auditors see every key; members of a key's `group` see
theirs. The list shows id, name, description, group, expiry, last used and
status. The detail page shows metadata plus usage aggregated per (source IP,
generation, day): first and last seen, lookups, misses. That aggregate, not the
raw per-lookup audit log, answers "which hosts still run the old image".
Actions: rotate (new secret shown once; the previous one enters its grace
window), clear previous secret, revoke, edit description, group and expiry.

**Rotation report.** A per-key page, the thing an operator opens after
rotating: every source IP that used the key in the last
`nss.usage_retention` (proposed 90 days), one row each, with first seen,
last seen, lookups, and which generation it last presented. Rows are grouped
`switched` (last seen on the current generation), `not switched` (last seen
on the previous one) and `gone` (no lookups since the rotation), with
counts in the header, so "12 of 140 hosts still on the old secret" reads at
a glance. Filter and sort by group; CSV export. The same aggregate feeds
`nss_key_rotation_cleanup` and the `nss_key_expiring` summary, so mail and
page always agree. Backed by an `nss_key_usage` table keyed on `(key_id,
source_ip, generation, day)` with lookup and miss counters and a
`last_seen` timestamp; `key_id` is a foreign key and `generation` the
integer the host presented, so no secret material is copied anywhere.
Updated by the lookup path, pruned at `nss.usage_retention`; the raw audit
log is not queried for this.

**Email, reusing the enrollment notification machinery** (`mail.*`,
`server/config/types_mail.go`). Keys gain `notification_email`, defaulting to
the creator's address; a team list is the intended value. No "mail every
group member": there is no group-mail concept, the group already has the UI,
and a list address is how teams want it. Events, each a template under the
existing `mail.template_dir` override scheme:

| Event | When | Content |
| --- | --- | --- |
| `nss_key_expiring` | same cadence and `mail.expiry_reminder_lead` as enrollment reminders | expiry date, per-IP usage summary |
| `nss_key_rotation_cleanup` | silent for `mail.rotation_quiet_period` after a rotation (default 7 d, the time a fleet roll is expected to take); then a daily digest while any host still presents the previous secret; stops when they drain or grace ends | a cleanup reminder, not a usage report: source IPs still on the old secret, last seen, days of grace left |
| `nss_key_expired_attempt`, `nss_key_revoked_attempt` | rate-limited by `mail.expired_attempt_window` | source IPs presenting a dead key |
| `nss_key_rotated`, `nss_key_revoked` | immediately | actor, time, grace expiry |

The only recurring mail is the rotation cleanup digest, deliberately quiet
while the roll is in progress. No general usage digest; the rest fires only
when something needs doing, and all of it is off when `mail.enabled` is
false.

### Server configuration

`nss.enabled` gates the feature. Config also sets: `auto_private_groups`
mode, `tombstone_retention`, defaults for home and shell when LDAP lacks
them, `allowed_shells` (names) for user self-service, an optional
source-address allow list. Lookups for privileged or
local-system names, and a single key seen from an unexpected number of
source addresses, are alerted on.

## Part B: host package (`mnestor/ssoossh-nss`)

A sibling of `ssoossh-pam` with the same shape: C module, Go daemon, nfpm
`.deb`/`.rpm`/`.apk`, `ssoossh-nss-selinux` (socket in `/run/ssoossh`,
outbound HTTPS, nothing else), fuzz targets, a pinned `SSOOSSHD_COMPAT`. It
depends on the server only through `docs/wire-contract.json` and the TLS pin.

### Daemon: cache

Option names and defaults follow `sssd.conf(5)` so operators moving a host
from sssd change the provider, not their mental model.

| Option | Default | Behaviour |
| --- | --- | --- |
| `entry_cache_timeout` | 5400 s | entry validity |
| `entry_cache_user_timeout`, `entry_cache_group_timeout` | inherit | per-map |
| `entry_negative_timeout` | 15 s | "not found" memory |
| `entry_cache_nowait_percentage` | 50 | past this share of TTL, answer from cache and refresh in the background |
| `refresh_expired_interval` | 0 in memory mode; 75 % of TTL in persistent mode | background sweep, jittered, bounded by `refresh_rate` (2/s) |
| `cache_expire_offline` (ssoossh) | 0 = never, as sssd | hard cap on serving stale entries while the server is unreachable |
| `login_soft_ttl`, `login_refresh_timeout` (ssoossh) | 60 s, 1 s | sshd-aware synchronous refresh, below |

Concurrent misses for the same name share one upstream request
(singleflight). `memcache_timeout` (sssd's mmap fast cache) is v2.

**Persistence, sssd-shaped.** `cache_mode: persistent` is the default
(sssd always keeps a disk cache); `cache_mode: memory` is the opt-in for
ephemeral images that want no identity state on disk. Layout
`/var/lib/ssoossh/db/cache.db` (SQLite, WAL), root-only 0600 in a 0700
directory. Trust model is sssd's: entries are stored as fetched over pinned
TLS and trusted on load; filesystem permissions are the boundary, and every
sshd login forces a fresh fetch anyway.
`ssoossh-nss-cache` is the `sss_cache` equivalent (`-E`, `-u`, `-g`, `-U`,
`-G`; expiry marks stale, never deletes). A host that reboots while the
server is down still resolves its known users from the persisted cache.

### Group changes and login freshness

- A running session's group vector is fixed at its own `initgroups()`;
  nothing in NSS changes it. New sessions pick up changes, as with sssd.
- Cached users refresh on TTL, triggered by whatever next looks them up.
- At login, sshd runs `AuthorizedPrincipalsCommand` (`ssoossh host
  principals`) before `do_setusercontext()` calls `initgroups()`. `host
  principals` sends a fire-and-forget `refresh <user>` op to the socket
  (50 ms timeout, result ignored; it still never touches the network). New
  sessions start fresh.
- `Match Group`/`AllowGroups`/`DenyGroups` are evaluated earlier, at
  `ga_init()`, via `initgroups_dyn` plus `getgrgid`; they work from the cache
  because responses carry gid+name. To make them fresh too, the daemon reads
  `SO_PEERCRED`, and when `/proc/<pid>/comm` is `sshd` and the entry is older
  than `login_soft_ttl`, refreshes synchronously within
  `login_refresh_timeout` before answering `getpwnam`; on timeout it answers
  from cache. On by default.

A `DenyGroups` change lands within one entry cache timeout. Instant
revocation remains the certificate.

### sssd-compatible options (v1)

| Option | Default | Semantics (as sssd) |
| --- | --- | --- |
| `min_id`, `max_id` | 1000, 0 | out-of-range entries rejected and logged |
| `override_gid` | unset | force one primary gid; beats `gidNumber` and private groups |
| `auto_private_groups` | `hybrid` | `true`: gid = uid always (existing `gidNumber` becomes supplementary); `false`: require `gidNumber`; `hybrid`: only when `gidNumber == uidNumber` and no real group owns that gid. Resolved server-side; a collision in `true` mode rejects the user rather than shadowing |
| `override_homedir`, `fallback_homedir` | unset, `/home/%u` | `%u` user, `%U` uid, `%d` domain, `%h` original |
| `override_shell`, `shell_fallback`, `allowed_shells`, `vetoed_shells` | unset, `/bin/sh`, `/etc/shells`, empty | |
| `case_sensitive` | `true` | |
| `ignore_group_members` | `false` | skip member lists; `initgroups` unaffected |
| `filter_users`, `filter_groups` | `root` | never sent upstream |
| `require_groups` (ssoossh) | unset | hard NSS filter: only members resolve on this host. Cost: files owned by non-members show a bare uid and `Match Group` cannot name them. Pair with sshd `AllowGroups`; not a substitute for it |
| `dns_discovery_domain` | host's domain | `_ssoossh._tcp.<domain>` SRV; `server` overrides. Trust is unchanged: a spoofed record points at a server the daemon refuses |
| `timeout`, `reconnection_retries`, `offline_timeout` | 5 s, 3, 60 s with backoff | |

Per-host overrides (`sss_override` equivalent): `/etc/ssoossh/nss-overrides.yaml`,
same shape as `principals.yaml`, per-user uid/gid/home/shell applied after the
server's answer and before the sanity rules; managed with
`ssoossh-nss-cache override`; never able to lower a uid below `min_id`.

### Daemon: sanity rules and hardening

Enforced on every response before caching: uid and gid within
`min_id..max_id`; name absent from `/etc/passwd` and `/etc/group` (`files`
precedes `ssoossh` in `nsswitch.conf` regardless); home absolute after the
override/fallback rules; shell in `/etc/shells` and not vetoed; name matches
`^[a-z_][a-z0-9_-]{0,31}$`.

Upstream: connect 2 s, total `timeout`; concurrency cap 8, then `UNAVAIL`;
private CA or SPKI pin only; proxy and cert env ignored; `User-Agent` carries
the version. `api_key_file: /etc/ssoossh/nss.key` (0600, `key_id` and `key_secret` on two
lines), sent only to the pinned endpoint, never logged or cached. On 401 the daemon stops fetching,
logs loudly, and serves its cache as if offline until `cache_expire_offline`.

Runs as root under systemd with `ProtectSystem=strict`, `PrivateTmp`,
`NoNewPrivileges`, empty `CapabilityBoundingSet`, `RuntimeDirectory=ssoossh`.
Config `/etc/ssoossh/nss.yaml`, root 0600, env not consulted. Socket
`/run/ssoossh/nss.sock` 0666 in a root-owned 0755 directory. No `getpwent`.

### NSS module: `libnss_ssoossh.so`

Connect, send one request, read one fixed-size reply, copy into the caller's
buffer, return. Entry points: `_nss_ssoossh_getpwnam_r`, `getpwuid_r`,
`getgrnam_r`, `getgrgid_r`, `initgroups_dyn`; `setpwent`/`getpwent`/`endpwent`
and group equivalents return `NSS_STATUS_UNAVAIL`.

```
request:  u8 version(1) | u8 op | u32 key_len | key bytes
          op: 1=passwd-by-name 2=passwd-by-uid 3=group-by-name
              4=group-by-gid 5=initgroups 6=refresh-user
          op 6 is fire-and-forget: status 0 at once, refresh in background

reply:    u8 version(1) | u8 status | u32 uid | u32 gid
          | u32 ngids | u32 gids[ngids]
          | u16 len name | bytes | u16 len gecos | bytes
          | u16 len home | bytes | u16 len shell | bytes
          status: 0=found 1=notfound 2=unavail 3=tryagain
```

Reply capped at 8 KiB. Buffer handling: compute the total from the four
string lengths plus `ngids * sizeof(gid_t)` before copying; too small →
`*errnop = ERANGE`, `NSS_STATUS_TRYAGAIN`; explicit-length copies,
NUL-terminated, pointers only into the caller's buffer; no `malloc`,
`strdup`, `printf`.

Status mapping: found → `SUCCESS`; not found → `NOTFOUND`; socket missing,
refused, timeout, bad version, oversize → `UNAVAIL`; try-again → `TRYAGAIN`.
`UNAVAIL` rather than `NOTFOUND` on failure matters: `NOTFOUND` makes a user
vanish as if authoritative; `UNAVAIL` lets `[UNAVAIL=continue]` behave and
`sudo` fail closed.

`SO_SNDTIMEO`/`SO_RCVTIMEO` 3 s total; no global state; fresh socket per
call; `MSG_NOSIGNAL`. Plain C11, `-fPIC -shared`, libc only.

## PAM: keep `pam_ssoossh` standalone

Do not route the PAM module through the NSS daemon. The module already has
most of what the split would buy (no key retention, no disk writes, 82 KB
with two vendored non-allocating libraries, hidden visibility plus a version
script, ASan/UBSan and libFuzzer, a console flow that prints URL, code and QR
through `pam_conv`), and it is packaged for musl, FreeBSD and macOS where a
Linux-only daemon would not exist. Two small changes inside the module, as
independent PRs ahead of this work:

- `proxy=` argument defaulting to `CURLOPT_PROXY ""` (proxy from the
  environment is deliberately left on today, `src/httpc.c`).
- `tls-ca-file=` wired to `CURLOPT_CAINFO`, optionally `tls-pin=` to
  `CURLOPT_PINNEDPUBLICKEY`.

Stale notes to clear while there: `src/pam_ssoossh.c:62` says console
endpoints do not exist (they do: `POST /api/certs/console`), and the README
lists console mode as unverified against a real server.

## Decisions

Taken 2026-10-09 and 2026-10-10:

- Module language: C.
- POSIX fields: separate `user_posix` table; `users` unchanged.
- Reverse lookup by uid (`/user/uid/:uid`): kept, unrestricted; the key is the control.
- SOC may revoke NSS keys and read their logs; not create or rotate.
- Cache timeouts: `entry_cache_timeout` 90 min; `entry_negative_timeout`
  15 s; `cache_expire_offline` never. sssd defaults throughout.
- `/api/ca`: unchanged.
- Service enrollment codes: left as is. A code is useless without the
  enrolled private key, which never reaches the server.
- PAM hardening PRs ship first; SELinux ships as `ssoossh-nss-selinux`.
- No server-side group gate, global or per key. Narrowing is host config.
- Response signing (2026-10-10): none. Plain JSON over pinned TLS with a
  fleet key, as sssd and hosted directories do. The JWS/JWKS layer, root pin,
  `cnf` holder binding and `x5c` mode from the earlier draft are removed.
- Routes: path parameters (`/user/name/:name`, `/user/uid/:uid`,
  `/group/name/:name`, `/group/gid/:gid`), not query strings; all-digit user
  and group names are legal, so the two lookups stay distinct.
- Rate limiting: per `(key_id, source IP)` with the connection's IP, never a
  header; per-key miss rate alerts, never blocks.
- Key headers: `X-Ssoossh-Key-Id` and `X-Ssoossh-Key-Secret`, Cloudflare
  service-token style; the id is a random slug fixed for the key's life;
  rotation replaces only the secret, with the previous one valid through a
  grace window.
- Key ownership: optional `description` and an assigned `group` per key
  whose members view, rotate, revoke and read its log; `admin.nss_key_group`
  creates keys and assigns groups.
- User self-service: shell only, chosen by name from `nss.allowed_shells`
  and sent as `shell_pref` beside the LDAP `shell`; the host resolves the
  name via `/etc/shells` or `shell_map` and falls back to the LDAP shell if
  it cannot honour the preference. Home directory is not user-settable.

## Provenance: what was verified and how

- Host-side commands: `client/cmd/host.go` and `client/cmd/host_principals.go`
  (`offline: true`, "must never touch the network").
- No POSIX attributes: grep for `uidNumber|gidNumber|homeDirectory|loginShell|posix`
  across `server/` returns nothing; `server/model/user.go`.
- Admin roles: `server/config/types.go:371-391` (`require_group`, `soc_group`,
  `auditor_group`).
- Client trust: `internal/apitypes/certrequest.go` ("a claim, not a control").
- PAM module: `ssoossh-pam/src/httpc.c`, `src/checks.c`, `src/qr.c`,
  `src/conv.c`, `selinux/pam_ssoossh.te`, `docs/pam_ssoossh.8`.
