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

- **Other principals.** OIDC yields a username, the directory holds the
  identifiers target systems actually know (`sAMAccountName`, a UPN, an
  admin or shared account). These feed the certificate's principal list the
  same way `OAuthFields.OtherAccounts` does today. Directories link a person
  to their alternate accounts in several shapes, and only the simplest is an
  attribute on the person's own entry; see "Account linking" below.
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
  # Field mapping, grouped by destination: everything that feeds a field is
  # declared under it. `attribute` reads the person's own entry (linking
  # topology 1); `searches` resolve linked accounts that are their own
  # directory entries (topologies 2-4) and run after the primary lookup.
  # A bare string is shorthand for `attribute: <string>`.
  fields:
    other_accounts:
      attribute: "sAMAccountName"    # forward list on the person's entry
      searches:
        - name: "admin accounts"
          base_dn: ""                # empty inherits ldap.base_dn
          filter: "(&(objectClass=person)(authorizedUser={{.Username}}))"
          value: "uid"               # attribute naming the linked account
    service_accounts:
      searches:
        - name: "owned service accounts"
          filter: "(&(objectClass=account)(owner={{.DN}}))"
          value: "uid"
    groups: "memberOf"               # DNs reduced to CN, or a name attribute
    department: "departmentNumber"   # any other key: extra template field,
                                     # same contract as OAuthFields.Extra

  sync:
    interval: 15m          # zero disables the sync job entirely
    disable_after: 3       # consecutive successful-search misses before disable
    reenable: true         # sync may clear its own disables on reappearance
    extra_groups: []       # persisted in addition to config-derived groups;
                           # see "user_groups" for the allowlist rule

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
  instead of claim names. The account fields additionally take `searches`,
  declared under the field they feed, so reading one field's block shows
  every source that populates it. The merge rule is per field: **a
  configured LDAP field (any `attribute` or `searches`) wins over the OIDC
  value; an unconfigured (empty) one leaves the OIDC value untouched.** Override rather than union, because union makes it
  impossible to retire a stale principal from only one source. Groups are the
  exception: both sources persist side by side (see storage), and the session
  identity's `Groups` remains the OIDC claim, per the settled decision that
  all six `service.Identity` fields ride the session.
- **The key identity fields are not changeable by LDAP.** Subject, username,
  and email come from OIDC and nowhere else: the subject keys the user row,
  the username is what LDAP lookups are keyed *by*, and the OIDC email claim
  is the source of truth for `users.email` (not unique across users, does
  not need to be; it is simply ours). Neither the login-time lookup nor the
  sync ever writes any of the three, so there is no `fields.username` or
  `fields.email`, and config validation rejects `username`, `email`, and
  `subject` as keys under `fields` rather than treating them as extra
  fields, since they could only read as an attempt to override identity.
- **Any other key under `fields` is an extra template field.** There is no
  `extra:` sub-map; LDAP enrichment is extra by definition. A key that is
  not one of the reserved names (`other_accounts`, `service_accounts`,
  `groups`) captures into the `OAuthFields.Extra` contract: same template
  namespace (`{{.Extra.<name>}}`), missing attributes store empty and
  render as MISSING, login never fails over one. Extra fields take the same
  shapes as the reserved ones (bare string, or `attribute` / `searches`),
  so a search's result list can feed a template field too. The reserved
  names are unavailable as extra field names, and `username`, `email`, and
  `subject` are rejected outright (see the key-identity bullet above).

## Account linking

A person's alternate accounts appear in directories in four shapes, and the
config must express all of them:

1. **Forward list on the person's entry.** The user's own entry carries a
   multi-valued attribute of account names. Covered by the field's
   `attribute` alone; no search.
2. **Reverse link by username.** The alternate account is its own entry, and
   it carries a multi-valued attribute of the usernames allowed to use it.
   Expressed as a `searches` entry under the field whose filter references
   `{{.Username}}`; each matched entry contributes its `value` attribute.
3. **Reverse link by another identifier.** Same as 2, but the linking
   attribute holds some other unique identifier from the main account
   (an employee number, a UUID) rather than the username. The filter
   references `{{.Attr.<name>}}`, an attribute of the primary entry. The
   server collects every attribute name referenced this way across all
   search templates and requests them in the primary lookup automatically,
   so nothing needs duplicating as an extra field.
4. **Reverse links with roles.** The alternate account entry distinguishes
   its owner from others authorized to use it, via different attributes.
   Expressed as searches over the same entries with different filters,
   placed under different fields: an ownership link (`owner={{.DN}}` is a
   common shape, so the primary entry's DN is in the template context)
   placed under `service_accounts` grants manage-and-enroll, while an
   authorized-user link under `other_accounts` grants a usable principal.
   Which link means which is the operator's call, made by where the search
   is placed.

Search templates see the OIDC identity fields, `{{.DN}}`, and
`{{.Attr.<name>}}` from the primary entry, all RFC 4515 escaped like the
primary filter. Within LDAP, everything under one field unions and dedupes:
its `attribute` plus each of its `searches`. The per-field override rule
from "Configuration" then applies to the combined result: configuring any
LDAP source for a field makes LDAP win over the OIDC claim for it.

Secondary searches are by filter, not DN, so they must re-run at sync time;
a reverse link can change without the person's own entry changing, which is
much of what the sync exists to catch. If an individual search fails during
an otherwise-successful pass, the cached values for its target field are
kept rather than shrunk: a transient error must not quietly narrow a
principal list.

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
"everyone in soc" is one indexed query.

**Only group names the config defines are persisted.** The allowlist is the
union of every group name the configuration references:
`admin.require_group`, `admin.soc_group`, `admin.auditor_group`, group names
appearing in certificate policy, plus the explicit `ldap.sync.extra_groups`
list for names nothing else references yet (a future notification target,
say). Membership outside that set is discarded at capture time; the server
does not mirror the directory's group graph, it records the roles it acts
on. The allowlist applies to both sources, including OIDC capture when LDAP
is disabled (the `extra_groups` knob may move out from under `ldap` if that
placement proves awkward).

A config change that adds a group name self-heals rather than needing a
backfill: LDAP-sourced rows repopulate at the next sync tick, OIDC-sourced
rows at each user's next login. Until then the new group simply has no
members recorded, which fails in the quiet direction (a notification to it
reaches nobody, and authorization never reads this table anyway).

Write path: OIDC groups are replaced (delete-and-insert for `source=oidc`) at
every login. LDAP groups are replaced at login (when the lookup succeeds) and
at every successful sync read, both filtered through the allowlist. A user
with LDAP rows whose entry disappears keeps the stale rows until the disable
lands; the disable clears fan-out eligibility anyway (disabled users are
excluded from recipient resolution).

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
   `base_dn`, read the configured attributes, then run each field's
   `searches` against the found entry.
   - Success: merge per the field rules, upsert `user_ldap` (DN, attributes
     including resolved account lists, `last_seen_at`, reset
     `consecutive_misses`), replace `source=ldap` group rows.
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
2. **Found:** re-run each field's `searches`, refresh `attributes` and
   `source=ldap` group rows, update `last_seen_at`, reset
   `consecutive_misses` (per-search failures keep that field's cached
   values, see "Account linking"). If the user is disabled with
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
- Re-enable rules key off it: the sync only clears disables whose source is
  exactly `ldap_sync`; operator re-enables clear anything, as today.
- The column is nullable and the migration performs **no backfill**. Users
  disabled before the upgrade carry NULL, which the sync's exact-match rule
  can never touch, the safe direction. Writing `admin` into old rows would
  behave identically, so the migration takes the cheaper path.

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
- **Kerberos GSSAPI bind.** Replacing `bind_dn` / `bind_password` with a
  keytab-based SASL GSSAPI bind is tracked separately in
  [ldap-gssapi-bind.md](ldap-gssapi-bind.md), which depends on this
  proposal. Its only demand on this work: the flat bind keys ship as the
  implicit "simple" mechanism, so a `bind.mechanism` block can slot in
  later without breaking existing configs.

## Limits

Multi-valued attributes are capped high rather than left unbounded: values
past the cap are dropped and the truncation is logged to the LDAP
destination. Starting defaults, tunable if a real deployment hits them:
1,000 values per multi-valued attribute, 1,000 matched entries per field
search, 64 KB for the serialized `user_ldap.attributes` JSON. Group
membership is naturally bounded by the allowlist, so no separate cap there.

The sync's cost scales as (users with a `user_ldap` row) times (1 + number
of configured field searches) directory operations per tick, which the
interval should be chosen against. Per-user search results are small; the
multiplier is the concern, not the payloads.

## Open questions

- **Group name reduction.** `memberOf` yields DNs; the config needs a rule
  for reducing them to comparable names (take CN, or a per-deployment
  template). lldap and AD differ here; the reference deployment (lldap)
  should drive the default. The allowlist comparison depends on this: the
  reduced name is what must match the configured group names.
