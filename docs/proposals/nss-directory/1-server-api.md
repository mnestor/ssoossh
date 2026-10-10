# Part 1: server API changes

**Repo:** `mnestor/ssoossh`, `server/` and `docs/`. **Depends on:** nothing.
**Consumers:** Part 2 (web UI), Part 3 (daemon). Shared context, threat model
and decisions are in [README.md](README.md).

## Scope

- `/api/nss/*` lookup endpoints, authenticated by fleet API keys.
- `user_posix` table, POSIX group storage, LDAP field destinations, sync.
- Fleet API key model, admin endpoints, role `admin.nss_key_group`.
- `nss_key_usage` aggregate, rotation report data, mail events.
- Shell self-service on `/api/users/me`.
- `nss.*` configuration, `make confdocs`, `make wire-contract`, swag.

Out of scope: any HTML or Svelte (Part 2), anything on the host (Part 3).

## Lookup endpoints

All inside the standard `{data, error, error_code}` envelope, with swag
annotations and a `docs/wire-contract.json` regeneration (`make
wire-contract`; the file is generated and bumps its own `version`).

| Path | Method | Auth | Returns |
| --- | --- | --- | --- |
| `/api/nss/user/name/:name` | GET | fleet key | user JSON; 404 `nss_not_found` |
| `/api/nss/user/uid/:uid` | GET | fleet key | same |
| `/api/nss/group/name/:name` | GET | fleet key | group JSON; 404 `nss_not_found` |
| `/api/nss/group/gid/:gid` | GET | fleet key | same |

`:name` is validated against `^[a-z_][a-z0-9_-]{0,31}$` after lower-casing
(see README R17); `:uid`/`:gid` must parse as `uint32`. Invalid input is 400
`nss_bad_request`, not 404, so the daemon does not negative-cache a typo as
an authoritative miss. All routes return 404 `nss_disabled` when
`nss.enabled` is false.

**User response**

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
  "key_id": "nss_k7m4qp2x"
}
```

- `groups`: every membership, primary first, gid and name together, so the
  daemon fills `initgroups`, `getgrgid` and `getgrnam` from one call.
- `home`: `homeDirectory`, else `nss.default_home` expanded, else null.
- `shell`: `loginShell` path, else `nss.default_shell`, else null.
- `shell_pref`: user's chosen shell **name** or null.
- `status`: `active` | `disabled` | `deleted`.
- `key_id`: the key that made the request, for log correlation.

**Group response**

```json
{"name": "sudo", "gid": 27, "members": ["mike", "alice"], "members_truncated": false}
```

`members` is capped at `nss.max_group_members` (default 2000); when the cap
applies, `members_truncated` is true and the server logs at warning.

**Error codes:** `nss_not_found`, `nss_bad_request`, `nss_disabled`,
`nss_unauthorized` (401, identical body and timing for unknown id and bad
secret), `nss_rate_limited` (429 with `Retry-After`).

## Data model

Timestamped migration pairs in both the sqlite and postgres trees
(single-owner directories per `AGENTS.md`).

**`user_posix`** (1:1 with `users`; `users` unchanged)

| Column | Type | Notes |
| --- | --- | --- |
| `user_id` | FK `users.id`, PK | |
| `uid` | uint32, unique | `uidNumber`, required |
| `gid` | uint32 | resolved primary gid after `auto_private_groups` |
| `home`, `shell` | text, nullable | from LDAP |
| `shell_pref` | text, nullable | user-set name |
| `gecos` | text | `displayName`, then `cn` |
| `status` | enum | `active`, `disabled`, `deleted` |
| `status_changed_at`, `created_at`, `updated_at` | timestamps | |

**`posix_groups`**: `gid` uint32 PK, `name` unique, `synthetic` bool (true
for auto private groups), timestamps. **`posix_group_members`**: `(gid,
user_id)` PK. Membership comes from `memberUid` or resolved `member` DNs.

**`nss_keys`**

| Column | Notes |
| --- | --- |
| `id` | random slug `nss_` + 8 base32 chars, PK, fixed for life |
| `current_generation` | int, 1 at creation, +1 per rotation |
| `secret_hash` | SHA-256 of the secret (R13) |
| `previous_secret_hash`, `previous_expires_at` | generation `current-1`, accepted until expiry (default `nss.key_rotation_grace` 720h) |
| `name`, `description`, `created_by` | |
| `group` | optional OIDC/LDAP group whose members manage this key |
| `expires_at` | required, default now + `nss.key_default_lifetime` (8760h) |
| `notification_email` | defaults to creator's address |
| `revoked_at`, `revoked_by` | |

**`nss_key_usage`**: PK `(key_id, source_ip, generation, day)`; `lookups`,
`misses`, `first_seen`, `last_seen`. Written by a flusher, not per request
(R14): the lookup path increments an in-memory map; a goroutine flushes it
every 60 s and on graceful shutdown with upserts. Pruned at
`nss.usage_retention` (2160h) by a scheduler job. A crash loses at most one
interval of counts, acceptable for a reporting table.

## Fleet key authentication

Headers `X-Ssoossh-Key-Id` and `X-Ssoossh-Key-Secret`. The secret is
`ssoossh_nss_` + 32 random bytes base64url (no padding). Verification:

1. Look up by id. Missing, revoked or expired: compute a dummy SHA-256 and
   compare anyway, then 401. Same body for every failure.
2. `subtle.ConstantTimeCompare(sha256(secret), secret_hash)`; on mismatch
   try `previous_secret_hash` if `previous_expires_at` is in the future.
3. Record generation presented (current or current-1) for usage.
4. Dead-key attempts (revoked, expired) feed `nss_key_revoked_attempt` /
   `nss_key_expired_attempt`.

Key headers are added to the request-logging redaction list; the id is
logged, the secret never.

## Rate limiting

Bucket on `(key_id, connection source IP)`: 20 req/s, burst 200
(`nss.rate_limit`, `nss.rate_burst`). The IP is the connection's, never a
forwarded header. Unauthenticated requests hit the existing per-IP limiter
from the cert-request routes first. Per-key miss rate above
`nss.miss_alert_rate` raises an alert (log + audit event), never a block.

## Directory sync and status

- **On demand.** An unknown name or uid triggers the LDAP lookup and upsert
  first login does, then serves from DB. Server-side negative cache 60 s.
  LDAP unreachable: serve from DB.
- **Background.** Scheduler job `nss_ldap_sync` re-reads every user and
  group with `uidNumber`/`gidNumber` every 15 min (`modifyTimestamp` where
  supported), full refresh nightly. Updates fields, membership and status;
  never deletes rows. The existing admin `ldap/sync` endpoint triggers it
  too (verify the route name before wiring).
- **No gate.** Every user with POSIX attributes is resolvable. Users without
  `uidNumber` are 404.
- **Status, not absence.** A known user stays resolvable until
  `nss.tombstone_retention` (2160h) after deletion (R11):

| Status | Served as | Notes |
| --- | --- | --- |
| `active` | normal | |
| `disabled` | name, uid, gid, gecos, home kept; `shell` = `nss.disabled_shell` (`/usr/sbin/nologin`); `status: disabled` | daemon sets passwd `!` |
| `deleted` | as `disabled` until `tombstone_retention`, then 404 | |

Group memberships are kept through `disabled`.

**Primary gid.** `nss.auto_private_groups` (`hybrid` default; `true`,
`false`) is resolved here against the full group table so every host gets
the same gid (R12). In `true` mode a collision rejects the user (logged,
404) rather than shadowing a real group.

## Shell self-service

`GET/PATCH /api/users/me` gains `posix` (`{uid, shell_pref}` or null when
the user has no POSIX record or NSS is disabled); PATCH accepts
`shell_pref` (string or null). The server
validates against `nss.allowed_shells` (names, default `[sh, bash, zsh,
fish]`), audit-logs changes, and returns 400 `shell_not_allowed` otherwise.
`GET /api/nss/shells` (session auth) returns the allowed list for the UI.
Home directory is not user-settable.

## Fleet key admin endpoints

Session-authenticated; swag-annotated; all mutations audit-logged.

| Path | Method | Who |
| --- | --- | --- |
| `/api/nss/keys` | GET | all keys: `nss_key_group`, admin, SOC, auditor; others: keys whose `group` they are in |
| `/api/nss/keys` | POST | `nss_key_group`, admin. Returns the secret once |
| `/api/nss/keys/:id` | GET | as list |
| `/api/nss/keys/:id` | PATCH | `description`, `expires_at`, `notification_email`: key group members too. `group`: `nss_key_group`, admin only |
| `/api/nss/keys/:id/rotate` | POST | `nss_key_group`, admin, key group members. Returns new secret once |
| `/api/nss/keys/:id/previous` | DELETE | same as rotate: clears previous secret early |
| `/api/nss/keys/:id/revoke` | POST | above plus SOC |
| `/api/nss/keys/:id/usage` | GET | read roles; rows `(source_ip, generation, day, lookups, misses, first_seen, last_seen)`, paginated |
| `/api/nss/keys/:id/rotation-report` | GET | read roles; per source IP: first/last seen, lookups, last generation, bucket `switched`/`not_switched`/`gone`, header counts; `?format=csv` |

List and detail responses carry a per-key `permissions` array for the
caller (`view`, `rotate`, `revoke`, `edit`, `assign_group`) so the UI does
not reimplement the role matrix.

Routes live under `/api/nss/keys`, not `/api/admin`, because key-group
members are not admins (README R16).

**Role.** `admin.nss_key_group` in `AdminConfig`
(`server/config/types.go`, beside `require_group` at line 371), with the
doc-comment + `mapstructure` + `default` convention, fail-closed like the
others. Admins inherit it.

## Mail

Reuses `mail.*` and the `mail.template_dir` override scheme. Templates:

| Event | When |
| --- | --- |
| `nss_key_expiring` | `mail.expiry_reminder_lead` before expiry (existing key) |
| `nss_key_rotation_cleanup` | silent for `mail.rotation_quiet_period` (**new key**, default 168h) after rotation; then daily while any IP still presents the previous generation; stops when drained or grace ends |
| `nss_key_expired_attempt`, `nss_key_revoked_attempt` | rate-limited by `mail.expired_attempt_window` (existing key) |
| `nss_key_rotated`, `nss_key_revoked` | immediately |

All off when `mail.enabled` is false. The rotation report endpoint and the
cleanup digest read the same aggregate query.

## Configuration

```yaml
nss:
  enabled: false
  auto_private_groups: hybrid
  tombstone_retention: 2160h
  default_home: /home/%u
  default_shell: ""            # empty: null in responses
  disabled_shell: /usr/sbin/nologin
  allowed_shells: [sh, bash, zsh, fish]
  max_group_members: 2000
  rate_limit: 20
  rate_burst: 200
  miss_alert_rate: 5           # misses/s per key
  usage_retention: 2160h
  key_default_lifetime: 8760h
  key_rotation_grace: 720h
  source_allow_list: []        # CIDRs; empty allows all
  privileged_name_alert: [root, admin, daemon, bin, sys]
  key_source_alert: 1000       # distinct source IPs per key per day
admin:
  nss_key_group: ""
mail:
  rotation_quiet_period: 168h
```

`make confdocs` regenerates the reference. Lookups for names in
`privileged_name_alert`, and one key seen from more than
`nss.key_source_alert` distinct IPs per day, raise audit alerts.

## Tests (table-driven, colocated)

- Key auth: unknown id, bad secret, revoked, expired, previous within and
  past grace; equal response bodies.
- Rate limiter keyed on `(key, connection IP)`; spoofed `X-Forwarded-For`
  ignored.
- Lookups: active, disabled, deleted in and past retention, no `uidNumber`,
  `auto_private_groups` all three modes plus collision, member cap.
- Validation: bad names and non-numeric uid return 400.
- Role matrix for every key endpoint.
- Usage flusher: counts aggregate, flush on shutdown, pruning.
- Rotation report bucketing.
- Migrations up/down on both DBs.

## Done when

`go test ./...` and `golangci-lint run` pass; `make wire-contract`,
`make confdocs` and swag output are regenerated and committed; a curl with a
valid key returns the JSON above against a test LDAP.
