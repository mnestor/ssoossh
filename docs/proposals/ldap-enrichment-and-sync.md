# LDAP enrichment, directory sync, and persisted groups

**Status:** proposal. Not scheduled.

**Anchors verified at:** `f948499`. `file:line` references drift; re-check
before relying on one.

`config.LDAPConfig` (`server/config/types.go`) is parsed but unconsumed, and
`model.User` ends with a TODO for LDAP-sourced fields. This proposal defines
what consuming it looks like: an enrichment lookup in the auth callback, a
background sync that keeps that data fresh and auto-disables users who leave
the directory, and the first persisted group storage, which also gives
notifications a group fan-out target.

## Principles (decided)

These were settled up front and shape everything below:

1. **LDAP is enrichment, never a requirement.** If directory data is
   available, the user gets more: extra principals, fresher groups,
   auto-disable coverage. If it is not, every basic operation still works on
   OIDC claims alone. Login therefore **fails open**: an LDAP error during the
   callback logs to the LDAP log destination and proceeds with the OIDC-only
   identity. There is no `required` knob.
2. **Both group sources persist.** OIDC groups are captured at every login;
   LDAP groups are captured at login and refreshed by the sync. Rows carry a
   `source` column so the two never collide and either can be queried alone.
3. **Auto-re-enable applies to auto-disabled users only.** If the sync
   disabled a user and the directory entry reappears, the sync may re-enable
   them. A user disabled by an admin or SOC operator is never touched by the
   sync, in either direction.
4. **Only known users sync.** A user must have logged in at least once (have a
   `users` row) to participate in sync, enrichment refresh, or group capture.
   The server never enumerates the directory. This keeps the user set
   self-selecting and lets later features (fan-out, directory views) build on
   a bounded, consented population.

## What the lookup is for

Two concrete needs drive the search:

- **Other principals.** The canonical case: OIDC yields a username, the
  directory holds the identifiers target systems actually know
  (`sAMAccountName`, a UPN, legacy account names). These feed the
  certificate's principal list the same way `OAuthFields.OtherAccounts`
  does today.
- **Usable groups.** Group membership for the roles and policies the server
  cares about (`admin.require_group`, `soc_group`, `auditor_group`, policy
  group references), kept current between logins by the sync, and usable by
  notifications ("send to everyone in soc").

## Configuration

Extend the existing `LDAPConfig` rather than replacing it. Sketch:

```yaml
ldap:
  enabled: true
  url: "ldaps://ldap.example.net"
  bind_dn: "cn=ssoossh,ou=service,dc=example,dc=net"
  bind_password: "..."
  base_dn: "ou=people,dc=example,dc=net"

  # Go template over the OIDC identity. Values are RFC 4515 escaped
  # automatically during rendering; the operator cannot opt out.
  user_filter: "(&(objectClass=person)(uid={{.Username}}))"

  # LDAP attribute names, mirroring config.OAuthFields claim names.
  fields:
    other_accounts: "sAMAccountName"   # multi-valued attribute -> principals
    service_accounts: ""
    groups: "memberOf"                 # DNs reduced to CN, or a name attribute
    email: ""
    extra:                             # attribute capture, same contract as
      department: "departmentNumber"   # OAuthFields.Extra

  sync:
    interval: 15m          # zero disables the sync job entirely
    disable_after: 3       # consecutive successful-search misses before disable
    reenable: true         # sync may clear its own disables on reappearance

  # Connection hygiene, absent from the current struct.
  timeout: 5s              # per-operation; the callback is a login path
  start_tls: false         # for ldap:// URLs
  tls_ca: ""               # PEM bundle path; empty uses system roots
  tls_insecure_skip_verify: false   # homelab escape hatch, logged loudly

  logging: ...             # exists today, unchanged
```

Notes:

- **Templatable filter.** `user_filter` becomes a Go template with the same
  idiom as key ID templates: `{{.Username}}`, `{{.Email}}`, `{{.Subject}}`,
  and `{{.Extra.<name>}}` (index syntax for awkward names). This is what
  "configure which OIDC attribute keys the search" means in practice: the
  operator references the field they want. Escaping per RFC 4515 is injected
  into template execution, not offered as a function, because a
  `preferred_username` containing `*` or `)` is otherwise filter injection.
- **Field mapping mirrors `OAuthFields`.** Same field names, attribute names
  instead of claim names. The merge rule is per field: **a configured LDAP
  field wins over the OIDC value; an unconfigured (empty) one leaves the OIDC
  value untouched.** Override rather than union, because union makes it
  impossible to retire a stale principal from only one source. Groups are the
  exception: both sources persist side by side (see storage), and the session
  identity's `Groups` remains the OIDC claim, per the settled decision that
  all six `service.Identity` fields ride the session.
- **`fields.extra`** follows the `OAuthFields.Extra` contract exactly:
  captured values land in the same template namespace
  (`{{.Extra.<name>}}`), missing attributes store empty and render as
  MISSING, login never fails over one.

## Storage

Two new tables, separate from `users` because their lifecycles differ from
the identity row and from each other.

### `user_ldap` (one row per user, sync bookkeeping)

| column               | notes                                              |
| -------------------- | -------------------------------------------------- |
| `user_id`            | PK, FK to `users.id`                               |
| `dn`                 | the entry's DN from the login-time search          |
| `attributes`         | JSON: the fetched `fields` values, incl. extra     |
| `last_seen_at`       | last successful read of the entry                  |
| `last_synced_at`     | last sync attempt that reached the directory       |
| `consecutive_misses` | successful searches that found no entry            |
| `created_at` / `updated_at` |                                             |

The `dn` is the load-bearing column. Login searches by filter once; the sync
re-reads **by DN**, which is cheaper and distinguishes "entry deleted" from
"filter no longer matches". A sync read by DN that fails falls back to one
filter search before counting a miss, so a moved entry re-anchors instead of
being disabled.

`attributes` doubles as last-known-good cache: if the login-time lookup fails
(fail open), enrichment falls back to the stored values from the previous
sync, so a directory outage degrades to slightly stale data rather than a
thinner certificate.

### `user_groups` (many rows per user, fan-out and display)

| column         | notes                                        |
| -------------- | -------------------------------------------- |
| `user_id`      | FK to `users.id`                             |
| `group_name`   |                                              |
| `source`       | `oidc` or `ldap`                             |
| `first_seen_at` / `last_seen_at` |                            |

Unique on (`user_id`, `group_name`, `source`). Rows rather than JSON so
"everyone in soc" is one indexed query. Store **all** groups from each
source, not an allowlist of policy groups: rows are cheap, and an allowlist
would require a backfill every time `admin.soc_group` or a policy changes.
A cap or allowlist knob can come later if a real deployment has a
pathological directory.

Write path: OIDC groups are replaced (delete-and-insert for `source=oidc`) at
every login. LDAP groups are replaced at login (when the lookup succeeds) and
at every successful sync read. A user with LDAP rows whose entry disappears
keeps the stale rows until the disable lands; the disable clears fan-out
eligibility anyway (disabled users are excluded from recipient resolution).

This table is useful even with LDAP disabled: OIDC group capture alone gives
notifications a fan-out target, just a staler one (per login instead of per
sync). The notifications feature should not require the LDAP feature.

## The invariant amendment

`docs/internals/invariants.md` currently states: "Group membership is never
persisted and never placed in a certificate." This proposal deliberately
retires the first half and must reword the invariant, not quietly violate it.
The surviving invariant:

> **Group membership never appears in a certificate, and persisted group
> rows are never an authorization input.** Authorization (admin, SOC,
> auditor, policy gates) is evaluated from the session identity only.
> `user_groups` is a snapshot for notification fan-out and display:
> it answers "who should this reach", never "may this caller do this".

The second sentence preserves the 2026-08-23 decision that the session
carries the identity fields and authorization does not hydrate from the
database per request. Nothing here reopens that.

Side effect worth naming: the sync partially closes the revocation window
described on `AdminConfig`. Today, removing a user from the identity provider
takes effect at their next login. With the sync, removing them from the
directory disables the account within `interval * disable_after`. Group
downgrades (still in the directory, out of a role group) still ride out the
session, unchanged.

## Login flow (auth callback)

1. OIDC callback verifies the ID token and builds the identity as today.
2. If `ldap.enabled`: render `user_filter` from the identity, search under
   `base_dn`, read the configured attributes.
   - Success: merge per the field rules, upsert `user_ldap` (DN, attributes,
     `last_seen_at`, reset `consecutive_misses`), replace `source=ldap` group
     rows.
   - Failure (connect, bind, search error): log to the LDAP destination,
     fall back to stored `user_ldap.attributes` if present, continue. Login
     never blocks on the directory.
3. Replace `source=oidc` group rows from the claim.
4. Session issuance proceeds unchanged; the session carries the merged
   identity fields exactly as it carries OIDC-only fields today.

The lookup adds one directory round trip to the login path, bounded by
`ldap.timeout`. That is the entire latency cost, and only when enabled.

## Sync job

A `job/scheduler.go` entry, running every `sync.interval` (zero disables).
Scope: every user with a `user_ldap` row, i.e. known users who have logged
in at least once with LDAP enabled. Never a directory enumeration.

Per user:

1. Read the entry by `dn` (fallback: one filter search to re-anchor a moved
   entry, updating `dn` on success).
2. **Found:** refresh `attributes` and `source=ldap` group rows, update
   `last_seen_at`, reset `consecutive_misses`. If the user is disabled with
   `disabled_source = ldap_sync` and `sync.reenable` is true, clear the
   disable.
3. **Not found (search succeeded, no entry):** increment
   `consecutive_misses`. At `sync.disable_after`, disable the user with
   `disabled_source = ldap_sync`.
4. **Directory unreachable or bind failed:** update nothing, count nothing,
   log loudly. An outage must never disable anyone; only a successful search
   that finds no entry is a miss.

Disabling reuses the existing machinery: `DisabledAt` blocks
authentication, and `service.SweepDisabledUserEnrollments` expires service
enrollments after `admin.disable_grace_period`. Two schema changes support
attribution:

- `users.disabled_source`: `admin`, `soc`, or `ldap_sync`. Complements
  `DisabledByUserID`, which is a FK to `users` and cannot represent the
  system actor (it stays NULL for `ldap_sync`).
- Re-enable rules key off it: the sync only clears disables it created;
  operator re-enables clear anything, as today.

Refresh semantics mid-session: `Identity.Extra` is re-hydrated from the
`users` row at approval time (`server/service/auth.go`), so extra-field
refreshes from the sync take effect for key ID templates without a re-login.
The account-list fields ride the session, so refreshed principals take
effect at the next login. Both behaviors are existing and unchanged; this
just documents that the sync inherits them.

## Notifications fan-out

With `user_groups` in place, a group-targeted notification is a recipient
resolver, not a new subsystem:

```
user_groups (group_name, any source) -> users (enabled, has email)
  -> notification_preferences (kind enabled or default)
```

New notification kinds (e.g. "SOC broadcast", "user auto-disabled") follow
the existing `notify` registry pattern: a `Definition` plus templates, no
schema change, per `model.NotificationPreference`'s design.

Accepted limitation: fan-out reaches only users who have logged in at least
once. That is principle 4, restated. The sync does not create shadow rows
for directory group members who have never authenticated; enumerating the
directory to email strangers is a different feature with different consent
implications, and it is out of scope.

## Out of scope / non-goals

- **Authorization from persisted groups.** Settled; see the invariant
  amendment.
- **Per-request DB hydration of identity fields.** Settled 2026-08-23; the
  session carries them.
- **Directory enumeration** and shadow users.
- **LDAP as an authentication source.** OIDC authenticates; LDAP only ever
  enriches.
- **Server-side key policy by directory attribute.** Adjacent, previously
  debated and dropped; nothing here depends on it.

## Open questions

- **Group name reduction.** `memberOf` yields DNs; the config needs a rule
  for reducing them to comparable names (take CN, or a per-deployment
  template). lldap and AD differ here; the reference deployment (lldap)
  should drive the default.
- **`disabled_source` backfill.** Existing disabled users predate the
  column. Backfilling `admin` is safe (the sync will then never touch them),
  and NULL can mean the same thing; pick one in the migration.
- **Multi-valued attribute limits.** A `memberOf` with thousands of values
  or a jumbo `attributes` JSON should be capped and logged rather than
  stored unbounded. Pick caps when writing the migration.
- **Whether sync refreshes `users.email`** when `fields.email` is
  configured. Leaning yes (it is just another override-configured field),
  but email drives notification delivery, so a directory typo has blast
  radius. Decide before implementation.
