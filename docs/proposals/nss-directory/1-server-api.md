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
- Shell self-service on `/api/users/me` (removed 2026-10-18 after the threat review: the shell comes from the directory and host policy only).
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
- `shell`: `loginShell` path, else `nss.default_shell`, else null. There is
  no user-chosen shell beside it (removed 2026-10-18 after the threat review: the shell comes from the directory and host policy only).
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
| `gid` | uint32, nullable | the mapped primary gid attribute, **frozen** after the first write (R38); `auto_private_groups` is applied on read so a config change is deliberate and a directory change is caught. NULL when the entry has no gid attribute, which only `auto_private_groups: "true"` accepts; under `true` it is a supplementary group and follows the directory, with no pending gid (decided 2026-10-18 by the owner: match sssd) |
| `pending_uid`, `pending_gid` | uint32, nullable | a differing directory value the sync refused to apply |
| `pending_since` | timestamp, nullable | first sync that saw the pending value |
| `home`, `shell` | text, nullable | mapped attributes; follow the directory |
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
`added_at` (nullable, added 2026-10-18 at the owner's request) is when
ssoosshd first stored the membership, since the directory does not
timestamp member values; NULL for one the first full import stored (the
run that records `nss.initial_import`). The writers store only the
difference from the stored set, so the value survives later syncs, an
accepted uid or gid change (a re-key updates the number in place), a
group rename and an account revive; a removal (a sync, a group
tombstone, a purge) deletes the row, and a membership added again gets
the new time.

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
`misses`, `first_seen`, `last_seen`, and `source_ip_key` (added 2026-10-19,
see "Every key" under Fleet key admin endpoints). Written by a flusher, not per request
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
  audit alert `nss_id_change_pending` at most daily per row. Every admin is
  also mailed once per change and weekly while it waits (added 2026-10-19;
  see Mail). The directory
  reverting the value clears the pending columns. Name, home, shell and
  gecos follow the directory on every sync. Without an id attribute, a
  moved entry (new DN) is seen as missing plus new; the new entry then
  collides on uid and name with the old row, which still exists (as
  missing, then as a tombstone), so neither is served until the old row is
  purged or ages out. Fail closed, and the reason to configure
  `ldap.id_attribute`. This holds for accounts only; groups are below.
- **Renamed or moved groups** (decided 2026-10-18 by the owner). A group
  renamed or moved with its gid kept is the same group, not a collision.
  When an entry matches no row by `directory_id` or `dn`, and the row
  holding its gid sits at another DN, the sync re-anchors that row on the
  entry if and only if the row's entry has gone: a full sync did not
  enumerate the row's DN, or (incremental and on-demand reads, the
  membership search of an account read included) a base-scope read of the
  row's DN with `nss.group_filter` returns nothing and a search for the gid
  finds no other entry. A directory error is never "gone"; the entry is
  then stored as today, and the next read tries again. Two entries carrying
  the gid, or a row and an entry with different non-empty `directory_id`
  values, are a collision as before. The re-anchored row keeps its id and
  gid (so its membership rows), takes the entry's DN, `directory_id`, name
  and members, loses `missing_since`, the tombstone and the collisions the
  rename resolves, and the move is audited as `nss.group_renamed` (old and
  new name and DN, gid, and `detected_by`: `full_sync_absence` or
  `base_read`). A new name another row holds is still a name collision:
  the row moves but keeps its old name and is withheld. Accounts keep the
  strict rule above, because a uid handed to a different person is what
  the tombstone exists to catch.
- **Duplicate name** (README R39). POSIX resolves one record per name, so
  two live entries whose mapped name attribute is equal are both withheld
  while it lasts, with `nss_name_collision` at most daily per name. A
  rename whose target is held by another row, a tombstone included, is
  the same collision: the renamed entry and the holder are both withheld
  until the directory is fixed or an admin purges the tombstone. User and
  group names are separate namespaces (a private group shares its user's
  name), so uniqueness is per table.

## Directory change audit

(decided 2026-10-18 by the owner) Every change the server makes to the
stored accounts, groups and memberships is an audit event, recorded in the
transaction of the write that makes it, so nothing about who joined or
left a group is lost when a group's membership rows are replaced. Events
the sync or a read records are the server's (`system`, no actor); those
an accepted id change or an early purge records carry the admin. An
account linked to a person has that person as the target. Every detail
has `table`, the row's `name`, `uid` or `gid`, `dn`, and `via`: one of
`full_sync`, `incremental_sync`, `on_demand` (a host's lookup), `login`
(the account read when a login links it), `tombstoned`,
`id_change_accepted` or `tombstone_purged`.

| Action | When | Detail beyond the common fields |
| --- | --- | --- |
| `nss.account_created`, `nss.group_created` | a row stored for the first time | account: `gid`; group: `member_count` (no members event goes with it) |
| `nss.account_renamed` | a matched account takes the name its entry now carries (matching stays strict) | `old_name`, `new_name`, `old_dn` |
| `nss.group_renamed` | a group re-anchored on a renamed or moved entry (above), or a matched group whose name changed in place | `old_name`, `new_name`, `old_dn`, `new_dn`, `detected_by` (`full_sync_absence`, `base_read`, `matched`), `name_blocked`, `revived` |
| `nss.account_missing`, `nss.group_missing` | the first full sync that did not see the row (`missing_since` set) | |
| `nss.account_tombstoned`, `nss.group_tombstoned` | a missing row tombstoned (`deleted_at` set) | `missing_since` |
| `nss.account_revived`, `nss.group_revived` | a missing or tombstoned row seen again | `missing_since`, `was_tombstoned` |
| `nss.group_members_changed` | one write changed one group's member set: a group read, an account read's membership search, a group tombstoned, an accepted uid change re-keying an account, a purged account | `added` and `removed` as `{uid, name}` sorted by uid, each at most 100 long, `added_count`, `removed_count` |
| `nss.initial_import` | a sync run into an empty table, once per table, in place of an event per created row | `created`; groups also `memberships` |

An event is written only when something changed, so a sync over an
unchanged directory writes none. `nss.tombstone_purged` is unchanged.

**History per row** (decided 2026-10-18 by the owner). Each stored
account's and group's page in the [viewer](#directory-viewer-auditor-and-above)
shows the audit events about it, so a group's page shows that a member
was added after the group was stored, and the member's page shows the same
event. `audit_events` is never filtered by its JSON payload, so every
event about stored rows also writes one `nss_audit_refs` row per row it
concerns, in the same transaction:

| Column | Notes |
| --- | --- |
| `event_id` | FK `audit_events.id`, `ON DELETE CASCADE`, so the retention and row-cap sweeps take a ref with its event (SQLite enforces foreign keys on every connection) |
| `kind` | `account` or `group` |
| `row_id` | `posix_users.id` or `posix_groups.id`: the random id, which survives a rename and an accepted uid or gid change where the name and the number do not. No foreign key: a purged row's refs stay until their events age out |
| `created_at` | the event's, so a page orders and seeks on this table alone |

PK `(event_id, kind, row_id)`; index `(kind, row_id, created_at, event_id)`
for the newest-first cursor read. What each event refers to:

| Action | Refs |
| --- | --- |
| the lifecycle events above (`created`, `renamed`, `missing`, `tombstoned`, `revived`, for accounts and groups) | the row |
| `nss.group_members_changed` | the group, and every added and removed account that is stored, including those past the 100 the detail lists |
| `nss.uid_collision`, `nss.gid_collision`, `nss.name_collision` | the holder, and the other entry when it is stored too |
| `nss.id_change_pending`, `nss.id_change_accepted`, `nss.tombstone_purged` | the row |
| `nss.name_mismatch` | the account |
| `nss.initial_import` | none: it is a summary |

The fleet key and lookup events (`nss.key_*`, `nss.lookup_forbidden`,
`nss.miss_rate_alert`, `nss.privileged_name_lookup`) are about keys and
hosts, not stored rows, and write none.

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
the same gid (R12). Each mode does what sssd does (decided 2026-10-18 by
the owner: match sssd; read from sssd 2.14.0-beta1-37-g493e37fe,
`sdap_save_user` in `src/providers/ldap/sdap_async_users.c`,
`sysdb_add_user` and `sysdb_add_group` in `src/db/sysdb_ops.c`,
`nss_get_object.c` and `nss_protocol_grent.c` in `src/responder/nss/`):

| Mode | gid attribute | Served gid | Groups list | Conflict |
| --- | --- | --- | --- | --- |
| `false` | required; an entry without one is skipped with a warning naming `auto_private_groups: "true"` | the entry's gid, served even when no group holds it | that group when one holds it | none |
| `hybrid` | required, as `false` | the entry's gid | the private group (the account's name and uid) when gid equals uid and no directory group holds it; otherwise the group holding the gid, if any | a directory group holding the gid is the primary group; one with the account's name leaves the gid unnamed (below) |
| `true` | optional; absent, zero or unparseable is stored as NULL | the uid | the private group first, then the entry's gid's directory group, if it is not the uid and a servable group holds it (sssd's `origPrimaryGroupGidNumber`), in gid order with the memberships | a directory group holding the uid as its gid, or the account's name: the account is not served (logged, 404) rather than shadowing a real group |

Where this differs from sssd, and why:

- `true` conflicts: sssd refuses whichever of the user and the group it
  caches second (`EEXIST`), so the winner depends on lookup order; here the
  directory group always wins.
- `true` with a gid no group holds: sssd's initgroups adds the bare number;
  `NSSMembership` carries a name with every gid (the wire contract), so it
  is left out.
- `hybrid` with gid equal to uid and a directory group of the account's
  name (another gid): sssd answers `getgrgid` with the private group and
  `getgrnam` with the directory group. `ssoosshd-nss` caches a group stub
  by name from the groups list, so a host would then answer `getgrnam`
  with the private gid; the private group is left out and the gid stays
  unnamed.
- A row stored without a gid under `true` and read under `hybrid` or
  `false` is not served (`not_served: no_gid`); sssd would never have
  stored it, and the next sync skips the entry.

## POSIX attribute mapping

Attribute names are configurable because directories differ (RFC 2307,
RFC 2307bis, AD with SFU or IdMU, FreeIPA) and because operators sometimes
keep POSIX data in custom attributes (README R35). `nss.attributes`:

| Key | Default | Used for |
| --- | --- | --- |
| `name` | `uid` | `posix_users.name`; must equal the `username` claim for a person to log in by their own name (R23) |
| `uid` | `uidNumber` | `posix_users.uid`; must parse as `uint32` |
| `gid` | `gidNumber` | primary gid before `auto_private_groups`; required unless it is `true` |
| `home` | `homeDirectory` | nullable; must be absolute when present |
| `shell` | `loginShell` | nullable |
| `group_name` | `cn` | `posix_groups.name` |
| `group_gid` | `gidNumber` | `posix_groups.gid` |
| `group_member` | `memberUid` | names; set `member` for DN-valued groups |
| `gecos_attributes` | `[displayName, cn]` | the only attributes the gecos template may read |

A value that does not parse (a non-numeric uid, a relative home) skips the
entry with a warning naming the DN; there are no partial rows. Every key
must be non-empty; config validation rejects an empty one.

**LDAP schema presets (decided 2026-10-18 by the owner).** One key,
`ldap.schema`, picks a preset (`rfc2307`, `rfc2307bis`, `active-directory`,
`lldap`, `freeipa`) that supplies `ldap.id_attribute`, `nss.user_filter`,
`nss.group_filter`, `nss.disabled_filter` and the `nss.attributes` names for
that directory. The presets are embedded YAML (`server/config/presets/`),
each citing its sources (sssd's attribute maps, lldap's source and PAM
guide, FreeIPA's source, Microsoft's filter docs). They merge as a layer
between `defaults.yaml` and the operator's file, so any key the file sets
wins. Unset applies nothing and leaves today's behaviour unchanged,
`ldap.id_attribute` included; an unknown value fails startup naming the
allowed ones. There is no migration or re-anchor tool yet: on a database
holding ids from another attribute, keep that `ldap.id_attribute` set
explicitly or start fresh. The user-docs page "LDAP schema presets" lists
every value, generated from the preset files.

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

None (removed 2026-10-18 after the threat review: the shell comes from the directory and host policy only). The design that stood here let a person pick a shell name
from a server list in the web UI, served beside the directory's shell for
the host to resolve. A person must not be able to pick the shell a host
serves them, so neither the shell nor the home directory is user-settable,
and `/api/users/me` carries no POSIX account.

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
| `/api/nss/keys` | GET | the keys whose `group` is one of the caller's groups, for everyone (changed 2026-10-19 by the owner: the roles that read every key use `/api/admin/nss/keys`) |
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

**Every key** (added 2026-10-19 at the owner's request).
`GET /api/admin/nss/keys`, auditor and above, beside the directory viewer
and like it a 404 (`nss_disabled`) with NSS off. One page of every key by
name then id, cursor paged (`after`, `limit`) with a total. Each row: id,
name, group, status, generation, expiry, previous secret expiry, revocation,
creation, the creator (`user_id`, `username`, `email`, `exists`, from a LEFT
JOIN on `users`, so a creator whose account is gone still names the id),
`hosts_7d` (distinct source addresses in 7 days), `last_seen_at` and the
recent usage alerts. Never a secret hash. Parameters:

- `q`: a case-insensitive substring of the name, the group or the creator's
  username or email; or, when it reads as an IP address or a CIDR range, an
  address search: the keys with `nss_key_usage` rows from that address or
  range within `nss.usage_retention`, each with `matched` (up to 5
  addresses, newest first, with `last_seen`, `generation` and `secret`
  `current`, `previous` or `older`, and the total). `address_search` echoes
  the normalised address or range and `usage_retention_seconds` the window.
  A term shaped like an address or a range that is not one is a 400 naming
  the problem.
- `status` (`all`, `active`, `expired`, `revoked`), `expiring_within` (7, 30
  or 90 days, active keys only), `unused` (no lookup in 30 days, never used
  included), `alerts` (a `miss_rate` or `key_source` claim in `nss_alerts`
  in 7 days, read by its primary key). Unknown values are dropped.

The address search reads `nss_key_usage.source_ip_key`, the address as its
16-byte IPv6 form (IPv4 as IPv4-mapped), BLOB on SQLite and BYTEA on
Postgres, written by the flusher beside `source_ip` and indexed with the day
(`idx_nss_key_usage_source_day`). One address is an equality and a range a
`BETWEEN` on the first and last address, both of which the two dialects
compare bytewise, so the search never scans the table. The host counts,
last use and matches are read for the page's keys only, a fixed number of
queries per page.

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
pending and collisions reads are not, except the two history reads. Each answers the `nss.enabled` 404
when NSS is off. Types: the "read-only directory viewer" block of
`server/webtypes/nss_admin.go`.

| Path | Method | Behaviour |
| --- | --- | --- |
| `/api/admin/nss/users` | GET | one page of `posix_users`, by name then id: id, name, uid, stored gid, gecos, dn, state, `last_synced_at`; `total`, `next_cursor` |
| `/api/admin/nss/users/:uid` | GET | the stored row in full, the linked person (users id, username, display name, `disabled_at`), every `posix_group_members` row for the uid by gid with the stored group's name and state and the row's `added_at` (added 2026-10-18), and `served` or `not_served`; 404 when no row has the uid |
| `/api/admin/nss/groups` | GET | one page of `posix_groups`, by name then id: id, name, gid, dn, state, membership row count, `last_synced_at`; `total`, `next_cursor` |
| `/api/admin/nss/groups/:gid` | GET | the stored row in full, its membership row count, and `served` (name, gid, member names served after `nss.max_group_members`, truncated) or `not_served`; 404 when no row has the gid |
| `/api/admin/nss/groups/:gid/members` | GET | one page of the group's `posix_group_members` rows by uid, each with the stored account's name and state when there is one and the row's `added_at` (added 2026-10-18); the cursor is the last uid; 404 when no group has the gid |
| `/api/admin/nss/users/:uid/audit`, `/api/admin/nss/groups/:gid/audit` | GET | the row's history (decided 2026-10-18 by the owner): one page of the events whose `nss_audit_refs` name the row, newest first, cursor-paged and shaped as `/api/admin/users/:id/audit` (`AuditEventsResponse`, an `after` off the history is a 400); 404 when no row has the number. Each read records `admin.audit_viewed` (scope `nss_account_timeline` or `nss_group_timeline`) to the shipped log only, as the user timeline does |

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
group holds the private group's gid or name) or `no_gid` (an account
stored without a gid attribute under `true`, read under `hybrid` or
`false`); `served` is absent exactly when it is set.

## Mail

Reuses `mail.*` and the `mail.template_dir` override scheme. Templates:

| Event | When |
| --- | --- |
| `nss_key_expiring` | `mail.expiry_reminder_lead` before expiry (existing key) |
| `nss_key_rotation_cleanup` | silent for `mail.rotation_quiet_period` (**new key**, default 168h) after rotation; then daily while any IP still presents the previous generation; stops when drained or grace ends |
| `nss_key_expired_attempt`, `nss_key_revoked_attempt` | rate-limited by `mail.expired_attempt_window` (existing key) |
| `nss_key_rotated`, `nss_key_revoked` | immediately |
| `nss_id_change_pending` (added 2026-10-19) | a uid or gid change turns pending: one message per admin per sync run, sent when the run ends, or per batch of on-demand and login reads, sent by a sweep every 5 minutes; lists only changes not mailed before |
| `nss_id_change_reminder` (added 2026-10-19) | Monday 09:00 server time while any mailed change is still pending; lists all of them with how long each has waited |

All off when `mail.enabled` is false. The rotation report endpoint and the
cleanup digest read the same aggregate query.

**Pending id change mail** (added 2026-10-19 at the owner's request). Both
kinds go to every admin: users whose OIDC groups as of their last login
(`user_groups`, source `oidc`) grant the admin role by
`AdminConfig.GrantsAdmin`, through the single seam
`NotificationService.StoredUserIsAdmin`, with an email and not disabled. No
configured address list; someone removed from the admin group at the IdP
keeps getting them until their next login. They are preferences, default
on, offered on the preferences page only to an admin session
(`Definition.AdminOnly`). Each lists at most 50 changes, oldest first, with
"and N more" and a link to `/admin/directory#pending-id-changes`. A change is
claimed per row on `posix_users.pending_notified_at` or
`posix_groups.pending_notified_at` with a guarded `UPDATE` that repeats the
pending numbers read; the column is cleared with `pending_since`, by an
accept, and whenever a different number turns pending, so a new change mails
again and an accepted or reverted one never does. Nothing is claimed while no
admin can be mailed. The weekly reminder is claimed on `nss_alerts`
(`id_change_reminder`, subject `directory`, six-day window) and lists only
changes already mailed once. Pending rows are read through the partial
indexes `idx_posix_users_pending` and `idx_posix_groups_pending` on
`pending_since`. No audit event is written for the sends, as for every other
mail: delivery logs "notification sent" per recipient.

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
- History: refs written for every event kind above, including members past
  the 100 cap; the two history routes page newest first, 404 an unknown
  number, admit auditors and above, and 404 with NSS off; the sweep takes
  refs with their events; a rename or accepted id change keeps one history.

## Done when

`go test ./...` and `golangci-lint run` pass; `make wire-contract`,
`make confdocs` and swag output are regenerated and committed; a curl with a
valid key returns the JSON above against a test LDAP, for a person and for
a service account that has never logged in.
