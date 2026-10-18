---
title: NSS directory
description: Serve POSIX users and groups from the directory to hosts running ssoosshd-nss, with fleet keys, frozen ids and fail-closed collisions.
sidebar:
  order: 11
---

The server half of host user lookups. With `nss.enabled`, `ssoosshd` keeps
the directory's POSIX accounts and groups in its database and answers
`ssoosshd-nss` on each host over `/api/nss/*`: a name or uid in, the account's
uid, primary gid, groups, home, shell and display name out. The host side --
installing the daemon, `nsswitch.conf`, SELinux, caching -- is on
[Directory users on a host](/ssoossh/hosts/nss/).

It is identity, never authorization. Nothing here issues, validates or
widens a certificate, no POSIX attribute ever appears in one, and the lookup
answer decides nothing on the server. What it changes on a host is which
accounts exist there; who may log in to them is still the certificate's
business.

## Enabling it

```yaml
ldap:
  enabled: true          # required
  url: "ldaps://ldap.example.net"
  base_dn: "dc=example,dc=net"
  id_attribute: entryUUID  # strongly recommended; see "Frozen uid and gid"
  # ... bind_dn, bind_password, as for enrichment

nss:
  enabled: true
  user_filter: "(&(objectClass=posixAccount)(memberOf=cn=linux-users,ou=groups,dc=example,dc=net))"
  group_filter: "(&(objectClass=posixGroup)(cn=linux-*))"
  disabled_filter: "(nsAccountLock=TRUE)"

admin:
  nss_key_group: "nss-key-admins"
```

**NSS requires LDAP.** `nss.enabled: true` with `ldap.enabled: false` fails
at startup. Every POSIX identity, people and shared or service accounts
alike, is a directory entry with a uid attribute; a shared account has no
OIDC token, so an identity-provider-only directory could never be complete,
and there is no claim source for POSIX data. The connection, `base_dn`,
`id_attribute`, `timeout` and TLS settings are the
[`ldap`](/ssoossh/operations/ldap/) block's.

The three filters are plain RFC 4515 filters, not templates. Their syntax is
checked when the server starts, and a typo stops the start. Attribute names
are configurable under
[`nss.attributes`](/ssoossh/reference/config/nss/attributes/), so RFC 2307,
RFC 2307bis, Active Directory and FreeIPA layouts all map: the defaults are
`uid`, `uidNumber`, `gidNumber`, `homeDirectory`, `loginShell` for accounts
and `cn`, `gidNumber`, `memberUid` for groups. Set `group_member: member` (or
`uniqueMember`) for groups that list members by DN.

Every key with its default is in the
[`nss` reference](/ssoossh/reference/config/nss/).

## What the sync stores

**Everything the filters match.** The full sync enumerates `nss.user_filter`
and then `nss.group_filter` under `ldap.base_dn`, with paged searches of 500,
and stores every entry it can parse. Unlike [LDAP enrichment](/ssoossh/operations/ldap/),
which reads only people who have logged in, this is a copy of a slice of the
directory, and the filter is the only control over how big that slice is.
Scope `nss.user_filter` to the people and accounts hosts should resolve, not
to `(objectClass=posixAccount)` across a whole enterprise tree: every
account it matches is resolvable by every host holding a fleet key.

Full sync was chosen over on-demand reads on purpose; see
[Decisions](/ssoossh/project/decisions/#declined-on-demand-only-directory-reads).

Two jobs keep it current, registered in `serve` and `serve api` (a `sign`
process has no scheduler), and every instance runs its own:

| Job | Every | Reads | Only it does |
| --- | --- | --- | --- |
| `nss-directory-incremental-sync` | [`nss.sync_interval`](/ssoossh/reference/config/nss/#sync_interval) (`15m`) | entries whose `modifyTimestamp` is no older than five minutes before the last successful run on this instance began | |
| `nss-directory-full-sync` | [`nss.full_sync_interval`](/ssoossh/reference/config/nss/#full_sync_interval) (`24h`) | every entry the filters select | notices entries that have gone, and clears collisions whose other entry has gone |

The first incremental run after a start is a full one, and so is any run the
directory refuses the `modifyTimestamp` filter for. Zero disables either job.
Admin → Directory's **Sync now** (`POST /api/admin/ldap/sync`) runs a full
NSS sync after the LDAP pass, except on a dry run, and reports its counts.
A lookup for a name or uid the database does not have reads that one entry
from the directory on the spot, so an account created a minute ago resolves
before the next sync; misses are remembered for 60 seconds per instance.

An entry is **skipped**, with a warning naming its DN and the attribute,
when its name is missing or not a valid POSIX name (systemd's relaxed rules:
no `:` or `/`, no control characters, not all digits, at most 32 bytes,
matched byte for byte and never lower-cased), when its uid or gid is missing
or not a decimal number, or when its home or shell is not an absolute path.
There are no partial rows. Each run ends with an info line, "nss directory
sync completed", carrying every count.

The display name is rendered from
[`nss.gecos_template`](/ssoossh/reference/config/nss/#gecos_template) over the
attributes listed in `gecos_attributes` only, and cleaned before any host
sees it (no `:`, no control characters, 256 bytes at most), since every local
user can read it with `getent`.

### Status

An account's status is derived each time it is read, never stored:

1. **deleted** when it is a tombstone (below);
2. else **disabled** when the person linked to it is disabled in ssoossh --
   by an admin, by SOC, or by the [LDAP sync](/ssoossh/operations/ldap/#the-sync)
   -- or when the entry matches
   [`nss.disabled_filter`](/ssoossh/reference/config/nss/#disabled_filter);
3. else **active**.

A disabled or deleted account is served with its name, uid, gid and home,
the shell replaced by
[`nss.disabled_shell`](/ssoossh/reference/config/nss/#disabled_shell), and
hosts lock it. A [SOC disable](/ssoossh/operations/roles/#disabling-a-user)
in the web UI therefore locks the person's account on every host at its next
refresh, with nothing else to do. A shared or service account with no linked
person answers only to the directory.

### Linking a person

When someone logs in and [LDAP enrichment](/ssoossh/operations/ldap/) finds
their entry (still a live directory read at every login), the server links
the POSIX row for that same entry to their ssoossh account, matching by
`directory_id` and then DN, never by uid alone. That link is what makes an
ssoossh disable reach hosts and what lets the person choose their own shell.
When the linked row's POSIX name differs from the person's `username` claim,
the `nss.name_mismatch` alert fires, at most daily: that person's own-name
certificate principal can never match their host account.

## Frozen uid and gid

Files on every host belong to the number, not the name, so a uid that moves
orphans everything its owner had, and a reused one hands it to whoever gets
the number next. The server therefore never follows a directory change of a
stored account's uid or primary gid, or of a group's gid:

- The sync keeps serving the stored number and records the directory's value
  as **pending**, with the time it was first seen. The
  `nss.id_change_pending` alert fires at most daily per row.
- The directory reverting the value clears the pending change.
- An admin accepts it explicitly. Only then do hosts see the new number,
  so re-own the files on hosts first (`chown -R`).

Name, home, shell and display name follow the directory on every sync.

The sync recognises an entry as the same row by `ldap.id_attribute` first,
then by DN. **Set `ldap.id_attribute`.** Without it a moved entry has a new
DN and looks like a missing entry plus a new one, and the new one collides
with the old row's uid and name, so neither is served until the old one is
purged.

Admin only, audit-logged as `nss.id_change_accepted`:

| Endpoint | Does |
| --- | --- |
| `GET /api/admin/nss/pending-id-changes` | accounts and groups with a pending change: name, DN, stored and pending numbers, since when |
| `POST /api/admin/nss/users/:uid/accept-id-change` | applies the account's pending uid and gid in one transaction and re-keys its memberships |
| `POST /api/admin/nss/groups/:gid/accept-id-change` | the same for a group, moving every account whose primary gid it was |

An accept is refused with 409 when another row, a tombstone included, holds
the new number. There is no reject: revert the directory, or the alert keeps
firing daily until someone decides. The web UI shows these as a Pending id
changes panel on Admin → Directory.

## Collisions and withheld accounts

`getpwnam` returns one record and `ls -l` one name, so two entries claiming
one uid, gid or name are a directory mistake or an attempt to receive
someone's logins. Either way the server serves **neither** while it lasts:

- Both rows are withheld, so every instance agrees, and lookups answer not
  found. A new entry presenting a held number or name is not stored at all.
- A rename onto a name another row holds keeps the old name and withholds
  both.
- The alert (`nss.uid_collision`, `nss.gid_collision` or
  `nss.name_collision`) fires at most daily per value, naming both entries.
- Account and group names are separate namespaces: a private group may
  share its account's name.

A collision clears when a read sees the pair no longer conflicts (the full
sync clears the ones whose other entry has gone), when an involved tombstone
is purged, or when an id change is accepted. `GET /api/admin/nss/collisions`
(admin) lists the current ones, both sides with name, DN, number and whether
it is a tombstone.

## Tombstones

An entry the full sync can no longer find by id, DN or filter becomes
**missing**; a directory that cannot be reached never counts as a miss.
After [`nss.tombstone_after`](/ssoossh/reference/config/nss/#tombstone_after)
(`168h`) missing, it is **deleted**: the account is served as disabled, the
group with no members. A tombstone keeps holding its number and name for
[`nss.tombstone_retention`](/ssoossh/reference/config/nss/#tombstone_retention)
(`2160h`), so neither can pass to a new entry while files on hosts still
belong to it; then the hourly `nss-tombstone-purge` job removes it, audited
as `nss.tombstone_purged`. An entry that reappears before then is restored.

To hand a name or number on deliberately, an admin purges the tombstone
early, also audited as `nss.tombstone_purged`:

```text
DELETE /api/admin/nss/users/:uid/tombstone
DELETE /api/admin/nss/groups/:gid/tombstone
```

Only a tombstone can be purged; a live row is 404. The Collisions panel on
Admin → Directory offers the same.

## Primary groups and shells

[`nss.auto_private_groups`](/ssoossh/reference/config/nss/#auto_private_groups)
decides each account's primary group on the server, so every host gets the
same gid: `hybrid` (the default: a private group only where uid equals gid
and no directory group holds that gid), `true` (every account gets one, and
an account whose uid is a directory group's gid is not served) or `false`.
Group answers list at most
[`nss.max_group_members`](/ssoossh/reference/config/nss/#max_group_members)
members (2000), flagged as truncated beyond that.

The server sends the directory's home (else
[`nss.default_home`](/ssoossh/reference/config/nss/#default_home), where `%u`
is the name) and shell (else
[`nss.default_shell`](/ssoossh/reference/config/nss/#default_shell)), and,
for a person, the shell *name* they chose from
[`nss.allowed_shells`](/ssoossh/reference/config/nss/#allowed_shells) in the
web UI's preferences (`PUT /api/users/me/shell`, audited as
`nss.shell_changed`). Each host maps that name to its own path, and decides
whether to honour it.

## Fleet keys

Every lookup carries a fleet API key, in the headers `X-Ssoossh-Key-Id` and
`X-Ssoossh-Key-Secret`. A key is not per host: one key serves a fleet, or
one environment of it, and is baked into the image. The
[security model](/ssoossh/concepts/security-model/#the-nss-fleet-key) covers
what that exposes.

- The id is `nss_` and eight base32 characters, fixed for the key's life and
  safe to log. The secret is `ssoossh_nss_` and 32 random bytes, base64url.
- The server stores only the SHA-256 of the secret. It is shown once, at
  creation and at each rotation.
- Every refusal -- unknown id, wrong secret, revoked, expired -- is the same
  401 `nss_unauthorized` with the same body and the same comparisons, so a
  caller learns nothing about which.
- A key lives at most
  [`nss.key_max_lifetime`](/ssoossh/reference/config/nss/#key_max_lifetime)
  (`8760h`), and exactly that unless its creator sets an earlier date. A
  later date sent to the API is trimmed to it, and the audit event records
  the date asked for as `requested_expires_at`.
- The expiry cannot be edited. Rotation renews it (see
  [Rotation](#rotation)), so a fleet keeps its key id by rotating before
  the expiry.
- Setting `nss.key_max_lifetime` to `0` removes the limit: a key created
  without a date then never expires. Changing the setting does not touch
  existing keys until their next rotation.

| Action | Endpoint | Who | Audit |
| --- | --- | --- | --- |
| List, view | `GET /api/nss/keys`, `GET /api/nss/keys/:id` | key admins, admin, SOC and auditors see every key; members of a key's group see that key | |
| Create | `POST /api/nss/keys` | key admins | `nss.key_created` |
| Edit description, notification address | `PATCH /api/nss/keys/:id` | key admins, members of the key's group | `nss.key_updated` |
| Assign the managing group | `PATCH /api/nss/keys/:id` (`group_name`) | key admins | `nss.key_updated` |
| Rotate | `POST /api/nss/keys/:id/rotate` | key admins, members of the key's group | `nss.key_rotated` |
| End the previous secret now | `DELETE /api/nss/keys/:id/previous` | the same | `nss.key_previous_cleared` |
| Revoke | `POST /api/nss/keys/:id/revoke` | key admins, members of the key's group, SOC, admin | `nss.key_revoked` |
| Usage, rotation report | `GET /api/nss/keys/:id/usage`, `GET /api/nss/keys/:id/rotation-report` | anyone who can view the key | |

These routes live under `/api/nss/keys`, not `/api/admin`, because the
members of a key's group are not admins. Each key in a response carries a
`permissions` list (`view`, `rotate`, `revoke`, `edit`, `assign_group`) for
the caller. A revoked key is terminal: every change to it is 409. The web UI
gives fleet keys pages of their own, outside the admin area, built on these
endpoints.

### Roles

[`admin.nss_key_group`](/ssoossh/reference/config/admin/#nss_key_group) is a
fourth group beside the three in [Roles and containment](/ssoossh/operations/roles/),
outside their chain: its members administer every key. Admins hold it
regardless, so empty narrows key administration to admins rather than
disabling it. SOC may revoke any key and read them all, but not create or
rotate; auditors read. A key's own group (`group_name`, any group the
session carries) lets a team manage its key without any of those roles.

### Rotation

Rotation keeps the id and replaces the secret. The previous secret stays
accepted for
[`nss.key_rotation_grace`](/ssoossh/reference/config/nss/#key_rotation_grace)
(`720h`), so hosts can be redeployed with the new key file at their own pace.

Rotation also renews the expiry: the key gets its own lifetime again from
the rotation, measured from its last rotation or its creation, and never
more than `nss.key_max_lifetime`. A key created for 30 days is renewed for
30 days; a key that never expired is given the limit if one is now set; with
the limit at `0`, a key that never expires stays that way.

An expired key can be rotated too, which brings it back with a new secret.
Its old secret gets no grace period: it had already stopped working, so
every host fails until it has the new key file.

1. Rotate, and write the new secret into the image or configuration
   management.
2. Watch the **rotation report**: per source address, the generation it last
   presented, bucketed `switched`, `not_switched` or `gone` (not seen since
   the rotation), with the days of grace left. `?format=csv` downloads it.
3. When `not_switched` is empty, end the previous secret (`DELETE
   .../previous`) rather than waiting out the grace.

The report reads the usage table: lookups and misses per key, source
address, generation and UTC day, counted in memory and flushed every minute
and at shutdown (a crash loses at most a minute of counts). Rows are kept
for [`nss.usage_retention`](/ssoossh/reference/config/nss/#usage_retention)
(`2160h`) by the hourly `nss-usage-prune` job.

## Source allow list and rate limits

Each lookup passes, in order:

1. **`nss.enabled`.** Off, every lookup is 404 `nss_disabled`.
2. **[`nss.source_allow_list`](/ssoossh/reference/config/nss/#source_allow_list)**,
   CIDRs, checked before the key is looked at. A request from anywhere else
   is 403 `nss_forbidden`, audited as `nss.lookup_forbidden` with its address
   and the key id it presented (at most hourly per pair). Empty allows all.
3. **The key.** A refused key spends a token from a per-address bucket at
   [`http.rate_limit`](/ssoossh/reference/config/http/#rate_limit) per
   `http.rate_duration`, and gets 429 once that is empty; otherwise 401.
4. **The key's own bucket**, per key and source address:
   [`nss.rate_limit`](/ssoossh/reference/config/nss/#rate_limit) lookups a
   second (20) with a burst of
   [`nss.rate_burst`](/ssoossh/reference/config/nss/#rate_burst) (200), for a
   host that resolves many names as it boots. Over it is 429
   `nss_rate_limited` with `Retry-After`; the daemon waits it out and serves
   its cache meanwhile.

The source address is the one the server observes, honouring forwarded
headers only from [`http.trusted_proxies`](/ssoossh/reference/config/http/#trusted_proxies)
-- the same address the audit log records. Behind a load balancer listed
there, each host keeps its own bucket; behind one that is not, every host
shares the balancer's, and the allow list sees only the balancer. See
[TLS and reverse proxies](/ssoossh/operations/tls-and-proxy/). The global
per-address limiter of the other routes does not apply to lookups. The
buckets are per instance, so N instances allow N times the rate.

A lookup for a name the database does not hold, while the directory cannot
be read, is 503 rather than a not-found the host would remember.

## Alerts

Every alert is a warning in the log and an audit event, deduplicated across
instances, and none of them blocks anything:

| Audit action | When | At most |
| --- | --- | --- |
| `nss.miss_rate_alert` | one key's not-found lookups exceed [`nss.miss_alert_rate`](/ssoossh/reference/config/nss/#miss_alert_rate) a second (5) over a minute, on one instance: a host enumerating names | daily per key |
| `nss.privileged_name_lookup` | a lookup by a name in [`nss.privileged_name_alert`](/ssoossh/reference/config/nss/#privileged_name_alert) (`root`, `admin`, `daemon`, `bin`, `sys`) | daily per key and name |
| `nss.key_source_alert` | one key seen from more than [`nss.key_source_alert`](/ssoossh/reference/config/nss/#key_source_alert) distinct addresses (1000) in a UTC day: far more than a fleet suggests a leak | daily per key |
| `nss.key_revoked_attempt`, `nss.key_expired_attempt` | a revoked or expired key's id is presented, whatever the secret | hourly per key |
| `nss.lookup_forbidden` | a lookup from outside the allow list | hourly per address and key id |
| `nss.uid_collision`, `nss.gid_collision`, `nss.name_collision` | two entries claim one number or name | daily per value |
| `nss.id_change_pending` | the directory changed a stored uid or gid | daily per row |
| `nss.name_mismatch` | a linked person's username differs from their POSIX name | daily per person |

Feed them to your SIEM with the rest of the [audit log](/ssoossh/operations/audit-log/).
Changes are audited too: `nss.key_*` for key administration,
`nss.id_change_accepted`, `nss.tombstone_purged`, `nss.shell_changed`.

## Mail

With [`mail.enabled`](/ssoossh/reference/config/mail/#enabled), each key's
notification address (the creator's by default; empty sends nothing) gets
`nss_key_expiring` (weekly inside `mail.expiry_reminder_lead`, then daily over
the final week), `nss_key_rotated` and `nss_key_revoked` at once,
`nss_key_expired_attempt` and `nss_key_revoked_attempt` at most once per
`mail.expired_attempt_window` between them, and `nss_key_rotation_cleanup`: a
daily digest of the addresses still presenting the previous secret, silent
for [`mail.rotation_quiet_period`](/ssoossh/reference/config/mail/#rotation_quiet_period)
(`168h`) after a rotation, stopping when every host has switched, the grace
ends or the key is revoked. None is a preference; see
[Email notifications](/ssoossh/operations/email-notifications/).

## Metrics

On the OpenTelemetry meter, when metrics are on:

| Metric | Kind | Attributes |
| --- | --- | --- |
| `ssoossh_nss_lookups` | counter | `kind` (user, group), `by` (name, id), `result` (hit, miss, unauthorized, rate_limited, forbidden, bad_request, error) |
| `ssoossh_nss_lookup_duration` | histogram, seconds | the same |
| `ssoossh_nss_keys` | gauge | `status` (active, expired, revoked) |

No key or name is an attribute, so cardinality stays bounded; per-key detail
is in the usage table.

## The secret is never logged

The access log records request headers by default, so the server takes
`X-Ssoossh-Key-Secret` off every request before the logger writes it, and the
lookup gate reads it from there. The key id is logged and audited; the
secret never appears in a log line, an audit payload or an error message,
and tests assert it through the real router. On the host, the HTTP client
the daemon shares with `ssoossh` redacts the header from its request trace.
