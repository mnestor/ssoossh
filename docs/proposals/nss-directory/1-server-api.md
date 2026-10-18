# Part 1: server API changes

**Repo:** `mnestor/ssoossh`, `server/` and `docs/`. **Depends on:** nothing.
**Consumers:** Part 2 (web UI), Part 3 (daemon). Shared context, threat model
and decisions are in [README.md](README.md).

## Scope

- `/api/nss/*` lookup endpoints, authenticated by fleet API keys.
- `posix_users` and POSIX group storage, configurable attribute mapping,
  the gecos template, directory sync and the login-time link to `users`.
- Config validation: `nss.enabled` requires `ldap.enabled` (README R36).
- `nss_enabled` in the settings payload and `is_nss_key_admin` on
  `/api/users/me` for Part 2 (README R25).
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

`:name` is matched byte for byte, never lower-cased, and validated with the
same relaxed rules `nss-systemd` applies before it calls any service
(README R23): non-empty, valid UTF-8, no control characters, no `:` or
`/`, no leading or trailing space, not all digits, not `.` or `..`, and at
most 32 bytes (the utmp field width; systemd itself sets no cap).
`:uid`/`:gid` must parse as `uint32`. Invalid input is 400
`nss_bad_request`, not 404, so the daemon does not negative-cache a typo as
an authoritative miss. All routes return 404 `nss_disabled` when
`nss.enabled` is false. The same validator runs at sync time, and an entry
whose name fails it is skipped with a warning naming the DN, so an
unresolvable account is visible in the log rather than silently absent.

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
  daemon answers `GetMemberships` by user and `GetGroupRecord` by name or
  gid from one call. Group members (`GetMemberships` by group) still need
  the group endpoint.
- `home`: `homeDirectory`, else `nss.default_home` expanded, else null.
- `shell`: `loginShell` path, else `nss.default_shell`, else null.
- `shell_pref`: user's chosen shell **name** or null.
- `gecos`: the rendered and sanitised `nss.gecos_template` output (see
  [POSIX attribute mapping](#posix-attribute-mapping)).
- `status`: `active` | `disabled` | `deleted`, derived as described under
  [Status](#status).
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

**`posix_users`** (anchored on the directory entry, README R22; `users`
unchanged)

| Column | Type | Notes |
| --- | --- | --- |
| `id` | text, PK | random id, same convention as `users.id`, so an accepted uid change is a column update |
| `uid` | uint32, unique | the mapped uid attribute, required; **frozen** after the first write (R38) |
| `name` | text, unique | the mapped name attribute, verbatim (R23); follows the directory |
| `directory_id` | text, indexed | the `ldap.id_attribute` value when configured, as `user_ldap.directory_id` |
| `dn` | text, unique | entry DN from the last successful read |
| `user_id` | FK `users.id`, nullable, unique | set at login (below); NULL for shared and service accounts |
| `gid` | uint32 | the mapped primary gid attribute, **frozen** after the first write (R38); `auto_private_groups` is applied on read so a config change is deliberate and a directory change is caught |
| `pending_uid`, `pending_gid` | uint32, nullable | a differing directory value the sync refused to apply |
| `pending_since` | timestamp, nullable | first sync that saw the pending value |
| `home`, `shell` | text, nullable | mapped attributes; follow the directory |
| `shell_pref` | text, nullable | user-set name; only meaningful with a `user_id` |
| `gecos` | text | rendered, sanitised template output (R35) |
| `dir_disabled` | bool | the directory's own disabled flag (`nss.disabled_filter`) |
| `missing_since` | timestamp, nullable | first sync that could not find the entry by id, DN or filter |
| `deleted_at` | timestamp, nullable | set when `missing_since` is older than `nss.tombstone_after`; cleared if the entry reappears |
| `created_at`, `updated_at`, `last_synced_at` | timestamps | |

There is no stored `status` column; see [Status](#status).

**`posix_groups`**: `id` text PK, `gid` uint32 unique and frozen (R38),
`pending_gid`, `pending_since`, `name` unique (same validator as user
names), `directory_id`, `dn`, `synthetic` bool (true for auto private
groups), timestamps. **`posix_group_members`**: `(gid, uid)` PK, keyed by
uid rather than `users.id` because members include accounts that never log
in. Membership comes from the mapped member attribute: `memberUid` values
are names, `member` values are DNs resolved against `posix_users.dn`.

**Linking a person.** When a login's LDAP enrichment resolves the person's
entry (by ID, then DN, then filter, `server/service/ldap.go:326`), the
server reads the mapped uid attribute from that entry and sets `user_id`
on the matching `posix_users` row, creating the row from the same read if
the sync has not seen it yet. Nothing else links: `users.subject` is never
read as a uid (README R37) and no claim carries POSIX data (R36). The sync
raises the audit alert `nss_name_mismatch` once per person whose
`users.username` differs from the linked row's `name`, because that
person's own-name certificate principal can never pass `getpwnam` (R23).

**`nss_keys`**

| Column | Notes |
| --- | --- |
| `id` | random slug `nss_` + 8 base32 chars, PK, fixed for life |
| `current_generation` | int, 1 at creation, +1 per rotation |
| `secret_hash` | SHA-256 of the secret (R13) |
| `previous_secret_hash`, `previous_expires_at` | generation `current-1`, accepted until expiry (default `nss.key_rotation_grace` 720h) |
| `name`, `description`, `created_by` | |
| `group` | optional OIDC/LDAP group whose members manage this key |
| `expires_at` | at most now + `nss.key_max_lifetime` (8760h), which is also the default; a later date is trimmed. NULL (never expires) only when the setting is 0 and no date is given. Not editable; each rotation renews it to the key's own lifetime from the rotation, capped by the setting, and an expired key's previous secret gets no grace (decided 2026-10-18) |
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

Neither the request log nor the audit log captures headers today, so there
is no redaction list to extend (README R34). The rule is simpler: the key
id may be logged, the secret never appears in any log line, audit payload
or error message, and the handler tests assert that a failed verification
logs the id only.

**Source allow list** (README R30). When `nss.source_allow_list` is
non-empty, the connection's source IP is checked against it before the key
is verified; a miss is 403 `nss_forbidden` with an audit event carrying the
IP and the presented key id. Empty allows all.

## Rate limiting

Bucket on `(key_id, connection source IP)`: 20 req/s, burst 200
(`nss.rate_limit`, `nss.rate_burst`). The IP is the connection's, never a
forwarded header. Unauthenticated requests hit the existing per-IP limiter
from the cert-request routes first. Per-key miss rate above
`nss.miss_alert_rate` raises an alert (log + audit event), never a block.

The bucket, the server-side negative cache and the miss-rate counter are
per instance; a multi-instance deployment behind a load balancer gets N
times the nominal budget, which is accepted (README R28).

## Directory sync

NSS requires LDAP: `nss.enabled: true` with `ldap.enabled: false` fails
config validation at startup (README R36). Every POSIX identity, people and
shared or service accounts alike, is a directory entry with the mapped uid
attribute.

- **On demand.** An unknown name or uid triggers one directory search
  (`nss.user_filter` AND the name or uid attribute), upserts the row, then
  serves from DB. Server-side negative cache 60 s per instance. LDAP
  unreachable: serve from DB.
- **Background.** Scheduler job `nss_ldap_sync` enumerates
  `nss.user_filter` and `nss.group_filter` under `ldap.base_dn` every
  15 min (`modifyTimestamp` where supported), full refresh nightly. It
  updates fields, membership, `dir_disabled`, `missing_since` and
  `deleted_at`; it never deletes rows before `tombstone_retention`. The
  existing admin `ldap/sync` endpoint triggers it too (verify the route
  name before wiring). Registered in `serve` and `serve api`; `sign` has
  no scheduler (README R28).
- **From a shell** (added 2026-10-18 at the owner's request).
  `ssoosshd ldap sync [--dry-run] [--json]` runs the endpoint's pass in
  its own process: the LDAP sync, then the NSS full sync unless it is a dry
  run. Neither job runs at startup, so a restart refreshes nothing. It is
  one more unelected instance against the shared database, never migrates
  it, and is audited as `ldap.sync_triggered` with the actor `local-cli`.
- **No gate.** Every entry with a uid attribute and a valid name is
  resolvable. Entries without the uid attribute, or whose name fails the
  validator, are skipped with a warning and are 404.
- **Duplicate uid** (README R29). Two entries carrying the same uid
  attribute: neither is served while the collision lasts (stored on the
  holding row as `withheld_at` and `withheld_reason`, so every instance
  agrees), and the audit alert
  `nss_uid_collision` fires at most daily per uid. Fail closed, because
  serving either one makes `ls -l` lie. A tombstoned row (`deleted_at`
  set) still holds its uid, so a new entry presenting that uid is a
  collision until `tombstone_retention` purges the row.
- **Frozen ids** (README R38). The sync matches an entry to a row by
  `directory_id` when `ldap.id_attribute` is configured, else by `dn`. A
  matched row's `uid` and `gid` never change on sync. When the entry's uid
  or gid attribute differs, the sync writes `pending_uid` or `pending_gid`
  with `pending_since`, keeps serving the stored values, and raises the
  audit alert `nss_id_change_pending` at most daily per row. The directory
  reverting the value clears the pending columns. Name, home, shell and
  gecos follow the directory on every sync. Without an id attribute, a
  moved entry (new DN) is seen as missing plus new; the new entry then
  collides on uid and name with the old row, which still exists (as
  missing, then as a tombstone), so neither is served until the old row is
  purged or ages out. Fail closed, and the reason to configure
  `ldap.id_attribute`.
- **Duplicate name** (README R39). POSIX resolves one record per name, so
  two live entries whose mapped name attribute is equal are both withheld
  while it lasts, with `nss_name_collision` at most daily per name. A
  rename whose target is held by another row, a tombstone included, is
  the same collision: the renamed entry and the holder are both withheld
  until the directory is fixed or an admin purges the tombstone. User and
  group names are separate namespaces (a private group shares its user's
  name), so uniqueness is per table.

## Status

`status` is derived at response time, never stored (README R24):

1. `deleted_at` set: `deleted`.
2. Else, the linked person's `users.disabled_at` set (admin, SOC or
   `ldap_sync` source alike), or `dir_disabled`: `disabled`.
3. Else `active`.

A SOC disable in the web UI therefore locks the host account on the next
sshd refresh (Part 3) with no new code path, and an unlinked shared or
service account answers only to the directory. `dir_disabled` comes from
`nss.disabled_filter`, an LDAP filter the entry is tested against on every
read (for example `(nsAccountLock=TRUE)` on 389-ds and FreeIPA, or
`(userAccountControl:1.2.840.113556.1.4.803:=2)` on AD); empty means the
directory never disables.

A known account stays resolvable until `nss.tombstone_retention` (2160h)
after `deleted_at` (R11):

| Status | Served as | Notes |
| --- | --- | --- |
| `active` | normal | |
| `disabled` | name, uid, gid, gecos, home kept; `shell` = `nss.disabled_shell` (`/usr/sbin/nologin`); `status: disabled` | daemon serves the record with `locked: true` (README U6) |
| `deleted` | as `disabled` until `tombstone_retention`, then 404 | |

Group memberships are kept through `disabled`.

**Primary gid.** `nss.auto_private_groups` (`hybrid` default; `true`,
`false`) is resolved here against the full group table so every host gets
the same gid (R12). In `true` mode a collision rejects the user (logged,
404) rather than shadowing a real group.

## POSIX attribute mapping

Attribute names are configurable because directories differ (RFC 2307,
RFC 2307bis, AD with SFU or IdMU, FreeIPA) and because operators sometimes
keep POSIX data in custom attributes (README R35). `nss.attributes`:

| Key | Default | Used for |
| --- | --- | --- |
| `name` | `uid` | `posix_users.name`; must equal the `username` claim for a person to log in by their own name (R23) |
| `uid` | `uidNumber` | `posix_users.uid`; must parse as `uint32` |
| `gid` | `gidNumber` | primary gid before `auto_private_groups` |
| `home` | `homeDirectory` | nullable; must be absolute when present |
| `shell` | `loginShell` | nullable |
| `group_name` | `cn` | `posix_groups.name` |
| `group_gid` | `gidNumber` | `posix_groups.gid` |
| `group_member` | `memberUid` | names; set `member` for DN-valued groups |
| `gecos_attributes` | `[displayName, cn]` | the only attributes the gecos template may read |

A value that does not parse (a non-numeric uid, a relative home) skips the
entry with a warning naming the DN; there are no partial rows. Every key
must be non-empty; config validation rejects an empty one.

**Gecos template.** `nss.gecos_template` (default `{{.DisplayName}}`) is
parsed and executed by the key ID template engine
(`server/service/keyid.go`: `text/template`, `missingkey=zero`, the `join`
function, and a validation execute at config load so a typo fails
startup). The data it sees is deliberately small:

| Field | Value |
| --- | --- |
| `.Name` | the mapped name attribute |
| `.DisplayName` | first non-empty of `gecos_attributes`, else `.Name` |
| `.Attr.<name>` | an attribute listed in `gecos_attributes`, first value; multi-valued attributes render with `join` |

Nothing outside `gecos_attributes` is reachable, so `userPassword`,
`sshPublicKey`, `description` and the like cannot end up in a field every
local user can read with `getent` unless the operator lists them on
purpose. Directory values are template data, never template text, so an
attribute value cannot inject a directive.

**Safeguarding the rendered value.** After rendering, the server:

1. replaces an invalid UTF-8 result with `.Name` and logs a warning;
2. replaces every control character (newline included) and every `:` with
   a space, then collapses runs of whitespace and trims;
3. caps the result at 256 bytes on a rune boundary;
4. substitutes `.Name` when the result is empty.

`:` is the passwd field separator and a newline is the record separator.
`nss-systemd` would mangle either on the host anyway
(`src/shared/user-record.c:265-293`), but a host must never see the
mangled form of something the server could have cleaned. An alteration is
logged once per entry at info with the DN. Names are not sanitised: a
name that fails the validator rejects the entry (above). The template
test table covers `:`, a newline, a bare control byte, invalid UTF-8, an
over-long value, an empty render, a multi-valued attribute with `join`,
and an attribute outside the allow list (renders empty, never the value).

## Shell self-service

`GET/PATCH /api/users/me` gains `posix` (`{uid, name, shell_pref}` or null
when no `posix_users` row is linked to the user or NSS is disabled); PATCH
accepts
`shell_pref` (string or null). The server
validates against `nss.allowed_shells` (names, default `[sh, bash, zsh,
fish]`), audit-logs changes, and returns 400 `shell_not_allowed` otherwise.
`GET /api/nss/shells` (session auth) returns the allowed list for the UI.
Home directory is not user-settable.

## Session flags for the web UI

Part 2 gates on two things Part 1 must expose (README R25): `nss_enabled`
in the settings payload beside `mail_enabled`
(`server/webtypes/webtypes.go:924`), and `is_nss_key_admin` beside
`is_admin`, `is_soc` and `is_auditor` on `/api/users/me`
(`webtypes.go:90-92`). Both carry the same `validate:"required"` tag as
their neighbours so the generated TypeScript includes them.

## Fleet key admin endpoints

Session-authenticated; swag-annotated; all mutations audit-logged.

| Path | Method | Who |
| --- | --- | --- |
| `/api/nss/keys` | GET | all keys: `nss_key_group`, admin, SOC, auditor; others: keys whose `group` they are in |
| `/api/nss/keys` | POST | `nss_key_group`, admin. Returns the secret once |
| `/api/nss/keys/:id` | GET | as list |
| `/api/nss/keys/:id` | PATCH | `description`, `notification_email`: key group members too. `group`: `nss_key_group`, admin only. The expiry cannot be edited; rotation renews it (decided 2026-10-18) |
| `/api/nss/keys/:id/rotate` | POST | `nss_key_group`, admin, key group members. Returns new secret once |
| `/api/nss/keys/:id/previous` | DELETE | same as rotate: clears previous secret early |
| `/api/nss/keys/:id/revoke` | POST | above plus SOC |
| `/api/nss/keys/:id/usage` | GET | read roles; rows `(source_ip, generation, day, lookups, misses, first_seen, last_seen)`, paginated |
| `/api/nss/keys/:id/rotation-report` | GET | read roles; per source IP: first/last seen, lookups, last generation, bucket `switched`/`not_switched`/`gone`, header counts; `?format=csv` |

List and detail responses carry a per-key `permissions` array for the
caller (`view`, `rotate`, `revoke`, `edit`, `assign_group`) so the UI does
not reimplement the role matrix.

## Pending id changes (admin)

Under `/api/admin`, admin role only, audit-logged (README R38):

| Path | Method | Behaviour |
| --- | --- | --- |
| `/api/admin/nss/pending-id-changes` | GET | users and groups with a pending uid or gid: name, dn, stored value, pending value, `pending_since` |
| `/api/admin/nss/users/:uid/accept-id-change` | POST | applies `pending_uid`/`pending_gid` to the row and re-keys `posix_group_members` in one transaction; fails 409 `nss_uid_collision` if the new uid is held by any row, tombstones included |
| `/api/admin/nss/groups/:gid/accept-id-change` | POST | same for a group's gid, re-keying `posix_group_members` and every `posix_users.gid` that referenced it |
| `/api/admin/nss/collisions` | GET | current uid and name collisions: the entries involved (name, DN, uid), which one is a tombstone, and since when |
| `/api/admin/nss/users/:uid/tombstone` | DELETE | purges a tombstoned row before `tombstone_retention`, freeing its uid and name (README R39); 404 unless `deleted_at` is set |

There is no reject: the operator reverts the directory, or the alert keeps
firing daily until someone decides. After an accept the old number is
free at once; the accept is the point of judgment, and the admin is
expected to have re-owned files on hosts first, since hosts see the change
only after it.

Routes live under `/api/nss/keys`, not `/api/admin`, because key-group
members are not admins (README R16).

**Role.** `admin.nss_key_group` in `AdminConfig`
(`server/config/types.go`, beside `require_group` at line 386), with the
doc-comment + `mapstructure` + `default` convention, fail-closed like the
others. Admins inherit it.

## Directory viewer (auditor and above)

(added 2026-10-18 at the owner's request)

A read-only view of the stored directory, under `/api/admin/nss` beside
the routes above but behind the auditor middleware (auditor implies SOC
implies admin, as the user directory), and not audit-logged, as the
pending and collisions reads are not. Each answers the `nss.enabled` 404
when NSS is off. Types: the "read-only directory viewer" block of
`server/webtypes/nss_admin.go`.

| Path | Method | Behaviour |
| --- | --- | --- |
| `/api/admin/nss/users` | GET | one page of `posix_users`, by name then id: id, name, uid, stored gid, gecos, dn, state, `last_synced_at`; `total`, `next_cursor` |
| `/api/admin/nss/users/:uid` | GET | the stored row in full, the linked person (users id, username, display name, `disabled_at`), every `posix_group_members` row for the uid by gid with the stored group's name and state, and `served` or `not_served`; 404 when no row has the uid |
| `/api/admin/nss/groups` | GET | one page of `posix_groups`, by name then id: id, name, gid, dn, state, membership row count, `last_synced_at`; `total`, `next_cursor` |
| `/api/admin/nss/groups/:gid` | GET | the stored row in full, its membership row count, and `served` (name, gid, member names served after `nss.max_group_members`, truncated) or `not_served`; 404 when no row has the gid |
| `/api/admin/nss/groups/:gid/members` | GET | one page of the group's `posix_group_members` rows by uid, each with the stored account's name and state when there is one; the cursor is the last uid; 404 when no group has the gid |

The lists take `after`, `limit` and `q` as the user directory does
(`server/utils/paging`), and `state`. `q` is a case-insensitive substring
of the name or the DN, and also an exact uid or gid when it parses as one.
An `after` that names no row, a members `after` that is not a uid, and a
`state` the list does not have are 400s.

**State.** One value per row, derived on read, never stored; the first
that holds wins: `withheld` (`withheld_at`), `tombstone` (`deleted_at`),
`disabled` (accounts only: `dir_disabled` or the linked
`users.disabled_at`), `missing` (`missing_since`), `pending`
(`pending_uid` or `pending_gid`), else `active`. The `state` filter is the
same rule as SQL, tested to agree with the Go function over every
combination of marks on both backends.

**Served.** What a host's lookup by the uid or gid gets now, built by the
lookup code (`servable`, `buildUser`, `primaryGroup`, `buildGroup`) on
`NSSLookupService`. It never calls the directory refresher, never writes
and never reads LDAP, so a row a host's miss would refresh is shown as the
database has it. `not_served` is `withheld`, `past_retention` (a tombstone
past `nss.tombstone_retention` the purge has not reached) or
`private_group_clash` (`nss.auto_private_groups: "true"` and a directory
group holds the private group's gid or name); `served` is absent exactly
when it is set.

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
  enabled: false               # requires ldap.enabled
  user_filter: (objectClass=posixAccount)
  group_filter: (objectClass=posixGroup)
  disabled_filter: ""          # empty: the directory never disables
  attributes:
    name: uid
    uid: uidNumber
    gid: gidNumber
    home: homeDirectory
    shell: loginShell
    group_name: cn
    group_gid: gidNumber
    group_member: memberUid
    gecos_attributes: [displayName, cn]
  gecos_template: "{{.DisplayName}}"
  auto_private_groups: hybrid
  sync_interval: 15m           # incremental (modifyTimestamp) sync; zero disables
  full_sync_interval: 24h      # full sync, the only run that finds deletions
  tombstone_after: 168h        # missing from the directory this long: deleted
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
  key_max_lifetime: 8760h
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

Validation at load: `nss.enabled` without `ldap.enabled` is an error;
`gecos_template` must parse and test-execute; every `attributes` key is
non-empty; `disabled_filter`, `user_filter` and `group_filter` must parse
as LDAP filters.

## Metrics

On the existing OpenTelemetry meter (README R31): an `ssoossh_nss_lookups`
counter with `kind` (user, group), `by` (name, id) and `result` (hit, miss,
unauthorized, rate_limited, forbidden) attributes; an
`ssoossh_nss_lookup_duration` histogram; an `ssoossh_nss_keys` gauge by
status. No per-key or per-name attributes, so cardinality stays bounded.

## Tests (table-driven, colocated)

- Key auth: unknown id, bad secret, revoked, expired, previous within and
  past grace; equal response bodies.
- Rate limiter keyed on `(key, connection IP)`; spoofed `X-Forwarded-For`
  ignored.
- Lookups: active, disabled by each source (admin, SOC, ldap_sync,
  `dir_disabled`), deleted in and past retention, no uid attribute,
  duplicate uid, `auto_private_groups` all three modes plus collision,
  member cap, a shared account with no `user_id`.
- Validation: each relaxed-rule failure, a 33-byte name, a mixed-case name
  matched exactly, and a non-numeric uid return 400.
- Linking: login resolves the entry and sets `user_id`; a second login does
  not re-link; `nss_name_mismatch` fires once.
- Frozen ids: a changed uid or gid attribute is held pending and the stored
  value keeps being served; the alert fires once per day; a revert clears
  it; accept applies it and re-keys memberships; accept onto a tombstoned
  uid is 409; a renamed entry with an id attribute keeps its uid; a moved
  entry without one collides and is not served.
- Unique names: two live entries with one name are both 404 and
  `nss_name_collision` fires once per day; a rename onto a held name
  withholds both; a tombstone holds its name; the purge frees it; purge of
  a live row is 404; user and group with the same name both resolve.
- Attribute mapping: every key remapped; unparsable uid and relative home
  skipped with a warning.
- Gecos template: parse error fails config load; the safeguarding table;
  allow-list enforcement.
- Config: `nss.enabled` without `ldap.enabled` rejected.
- Role matrix for every key endpoint.
- Usage flusher: counts aggregate, flush on shutdown, pruning.
- Rotation report bucketing.
- Migrations up/down on both DBs.

## Done when

`go test ./...` and `golangci-lint run` pass; `make wire-contract`,
`make confdocs` and swag output are regenerated and committed; a curl with a
valid key returns the JSON above against a test LDAP, for a person and for
a service account that has never logged in.
