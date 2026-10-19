-- The NSS directory: the POSIX accounts and groups served to hosts over
-- /api/nss/*, the collisions that withhold some of them, the fleet keys
-- those hosts authenticate with, the keys' usage
-- aggregate, the claim markers that hold each NSS alert to one per
-- window across instances, and the links from audit events to the rows
-- they concern. See docs/proposals/nss-directory/1-server-api.md
-- ("Data model") and server/model/posix.go and server/model/nss.go.
--
-- uid and gid are uint32 in Go. SQLite's INTEGER is a 64-bit signed integer
-- and holds every uint32. The Postgres copy of this file uses BIGINT for the
-- same columns, because Postgres has no unsigned type and its INTEGER stops
-- at 2^31-1, below the top half of the uid space (the choice
-- certificate_requests.caller_uid already made). The usage counters are
-- BIGINT there too, since a day's lookups at a raised nss.rate_limit can
-- pass 2^31.
--
-- There is no status column. Whether an account is active, disabled or
-- deleted is derived when it is read, from deleted_at, dir_disabled and the
-- linked users.disabled_at, so a SOC disable reaches hosts without a second
-- write that could fall out of step with the first.

-- One row per directory entry that carries the mapped uid attribute: people,
-- and the shared and service accounts that never log in, which is why this
-- is not 1:1 with users. Anchored on the entry (directory_id, then dn), with
-- an optional link to the person.
CREATE TABLE posix_users (
    -- A random id, the same convention as users.id, so an accepted uid
    -- change is a column update rather than a delete and an insert.
    id              TEXT PRIMARY KEY NOT NULL,
    -- Frozen after the first write. Hosts own files by number, so a
    -- directory that changes it is held in pending_uid until an admin
    -- accepts the change.
    uid             INTEGER NOT NULL,
    -- The mapped name attribute, verbatim. Follows the directory.
    name            TEXT NOT NULL,
    -- The ldap.id_attribute value, in the same form as
    -- user_ldap.directory_id, and the first anchor the sync matches on.
    -- Empty string when ldap.id_attribute is unset; the sync then matches
    -- on dn alone.
    directory_id    TEXT NOT NULL DEFAULT '',
    -- The entry's DN from the last successful read. Set to the empty string
    -- when an entry with another directory_id turns up at the same DN: two
    -- entries cannot share one, so this row's copy is stale, and the row is
    -- matched by directory_id alone from then on.
    dn              TEXT NOT NULL,
    -- The person, set at login when the LDAP enrichment resolves this
    -- entry. NULL for shared and service accounts and for anyone who has
    -- not logged in. Nulled rather than deleted with the user, because the
    -- account belongs to the directory, not to the login.
    user_id         TEXT NULL REFERENCES users(id) ON DELETE SET NULL,
    -- The mapped primary gid, frozen like uid. auto_private_groups is
    -- applied when the row is read, never stored here, so a config change
    -- is deliberate and a directory change is caught. NULL when the entry
    -- has no gid attribute, which only auto_private_groups "true" accepts
    -- (as sssd does); under "true" it follows the directory unfrozen.
    gid             INTEGER NULL,
    -- A differing directory value the sync refused to apply, and the first
    -- sync that saw one. The directory reverting clears all three; an
    -- admin accepting applies them.
    pending_uid     INTEGER NULL,
    pending_gid     INTEGER NULL,
    pending_since   DATETIME NULL,
    -- Mapped attributes, following the directory. NULL when the entry has
    -- none.
    home            TEXT NULL,
    shell           TEXT NULL,
    -- The rendered and sanitised nss.gecos_template.
    gecos           TEXT NOT NULL DEFAULT '',
    -- The directory's own disabled flag, from nss.disabled_filter.
    dir_disabled    INTEGER NOT NULL DEFAULT 0,
    -- The first full sync that did not see the entry (a directory outage or
    -- a failed search is never a miss). NULL while the entry resolves.
    missing_since   DATETIME NULL,
    -- Set once missing_since is older than nss.tombstone_after, cleared if
    -- the entry reappears. A tombstone still holds its uid and name, so
    -- neither can be handed on until nss.tombstone_retention purges the row
    -- or an admin does.
    deleted_at      DATETIME NULL,
    -- Set while the row is part of a uid or name collision, cleared when
    -- the collision resolves. Lookups treat a withheld row as not found.
    -- Row state rather than state of one sync run, so every instance, and
    -- one that has just restarted, withholds the same rows. The collisions
    -- themselves are in nss_collisions: a row is withheld while one names
    -- it, and withheld_reason sums them up in a short sentence for the
    -- operator ("uid 1234 is also carried by cn=bob,ou=people,dc=example").
    withheld_at     DATETIME NULL,
    withheld_reason TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL,
    -- The last sync or on-demand read that reached the directory for this
    -- entry. Every row is born from such a read, so it is never NULL.
    last_synced_at  DATETIME NOT NULL
);

-- getpwuid and getpwnam, and the uniqueness POSIX needs: one record per
-- number and one per name, tombstones included.
CREATE UNIQUE INDEX idx_posix_users_uid ON posix_users (uid);
CREATE UNIQUE INDEX idx_posix_users_name ON posix_users (name);
-- The sync's DN anchor, and where a group's member DNs are resolved.
-- Partial so the empty string of a released DN (above) can repeat.
CREATE UNIQUE INDEX idx_posix_users_dn ON posix_users (dn) WHERE dn <> '';
-- The login link: at most one account per person. NULLs are distinct in
-- both dialects, so unlinked rows never collide.
CREATE UNIQUE INDEX idx_posix_users_user_id ON posix_users (user_id);
-- Partial, as idx_user_ldap_directory_id: the empty string means "no ID"
-- and is not worth indexing.
CREATE INDEX idx_posix_users_directory_id ON posix_users (directory_id) WHERE directory_id <> '';

-- One row per directory group with the mapped gid attribute. Private groups
-- (nss.auto_private_groups) are worked out when an account is read and are
-- never stored, so every row here is a directory entry.
--
-- Groups go missing and are tombstoned the way accounts are: a group missing
-- from the directory for nss.tombstone_after is tombstoned (deleted_at), and
-- purged with the accounts after nss.tombstone_retention. A tombstoned group
-- still resolves by name and gid, so ls -l keeps showing a name for the
-- files it owned, but it is served with no members, so it grants nothing.
CREATE TABLE posix_groups (
    id              TEXT PRIMARY KEY NOT NULL,
    -- Frozen after the first write, as posix_users.uid.
    gid             INTEGER NOT NULL,
    pending_gid     INTEGER NULL,
    pending_since   DATETIME NULL,
    -- The mapped group name attribute, held to the same validator as user
    -- names. A separate namespace from posix_users.name: a private group
    -- shares its user's name.
    name            TEXT NOT NULL,
    directory_id    TEXT NOT NULL DEFAULT '',
    -- As posix_users.dn, including the empty string of a released DN.
    dn              TEXT NOT NULL,
    -- As posix_users: the first full sync that did not see the entry, NULL
    -- while it resolves; and the tombstone, set once missing_since is older
    -- than nss.tombstone_after and cleared if the entry reappears. The
    -- sync deletes a tombstoned group's membership rows.
    missing_since   DATETIME NULL,
    deleted_at      DATETIME NULL,
    -- As posix_users: set while the row is part of a gid or name collision.
    withheld_at     DATETIME NULL,
    withheld_reason TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL,
    -- The last sync or on-demand read that reached the directory for this
    -- entry. Every row is born from such a read, so it is never NULL.
    last_synced_at  DATETIME NOT NULL
);

-- getgrgid and getgrnam, and one record per number and per name.
CREATE UNIQUE INDEX idx_posix_groups_gid ON posix_groups (gid);
CREATE UNIQUE INDEX idx_posix_groups_name ON posix_groups (name);
-- The sync's two anchors, partial as posix_users' are: the empty string
-- means "no ID" in one and "released" in the other. A DN names one entry,
-- so it is unique, as posix_users.dn is.
CREATE INDEX idx_posix_groups_directory_id ON posix_groups (directory_id) WHERE directory_id <> '';
CREATE UNIQUE INDEX idx_posix_groups_dn ON posix_groups (dn) WHERE dn <> '';

-- Group membership by number. Keyed by uid rather than users.id because
-- members include accounts that never log in. No foreign keys: an accepted
-- uid or gid change re-keys these rows itself, in the same transaction that
-- changes the number, and a constraint would fix the order of those writes.
CREATE TABLE posix_group_members (
    gid      INTEGER NOT NULL,
    uid      INTEGER NOT NULL,
    -- When ssoosshd first stored this membership. The directory does not
    -- timestamp member values, so this is not the date the member was
    -- added there. NULL for a membership the first full import stored (the
    -- run that records nss.initial_import): present before that import.
    -- A later sync that leaves the membership in place, an accepted uid or
    -- gid change and a group rename keep it; only a removal clears it, and
    -- a membership added again gets the time it is stored again.
    added_at DATETIME,
    PRIMARY KEY (gid, uid)
);

-- The primary key answers "members of this group"; this answers "groups of
-- this user", the initgroups lookup.
CREATE INDEX idx_posix_group_members_uid ON posix_group_members (uid);

-- Directory entries that contend for a uid, gid or name a stored row holds.
-- POSIX resolves one record per number and per name, so while two entries
-- claim one, neither is served: the holder row is withheld, and the other
-- entry is either not stored at all (a new entry whose number or name is
-- taken, a tombstone's included) or is a stored row withheld as well (a
-- rename onto a held name). One row per pair; the sync writes it each time
-- it sees the pair, and deletes it once the pair is gone: when either entry
-- is read again without the clash, and on a full sync for every pair it did
-- not see. Nothing else reads it but the admin collisions report.
CREATE TABLE nss_collisions (
    -- uid or user_name for an account (holder_id is a posix_users.id), gid
    -- or group_name for a group (holder_id is a posix_groups.id).
    kind          TEXT NOT NULL CHECK (kind IN ('uid', 'user_name', 'gid', 'group_name')),
    -- The row holding the contested value. No foreign key: it names a row
    -- of one table or the other by kind, and every path that deletes such a
    -- row deletes its collisions in the same transaction.
    holder_id     TEXT NOT NULL,
    -- The other entry's DN as the directory returned it.
    other_dn      TEXT NOT NULL,
    -- The contested value as text: the number or the name.
    value         TEXT NOT NULL,
    -- The other entry's row when it is stored too, NULL when the sync
    -- refused to store it. A stored other row is withheld like the holder.
    other_id      TEXT NULL,
    -- What the other entry carried when last seen: its mapped name and its
    -- uid (for an account) or gid (for a group).
    other_name    TEXT NOT NULL DEFAULT '',
    other_number  INTEGER NOT NULL,
    first_seen_at DATETIME NOT NULL,
    last_seen_at  DATETIME NOT NULL,
    PRIMARY KEY (kind, holder_id, other_dn)
);

-- Re-reading an entry replaces the pairs it contends in, and deleting or
-- re-reading a holder replaces the pairs it holds.
CREATE INDEX idx_nss_collisions_other_dn ON nss_collisions (other_dn);
CREATE INDEX idx_nss_collisions_holder_id ON nss_collisions (holder_id);
CREATE INDEX idx_nss_collisions_other_id ON nss_collisions (other_id) WHERE other_id IS NOT NULL;

-- Fleet API keys: one per fleet of hosts that resolves accounts through the
-- server. The table is fleet-sized, tens of rows, so nothing but the primary
-- key is indexed.
CREATE TABLE nss_keys (
    -- "nss_" and 8 base32 characters, fixed for life.
    id                               TEXT PRIMARY KEY NOT NULL,
    -- 1 at creation, plus one per rotation.
    current_generation               INTEGER NOT NULL DEFAULT 1,
    -- SHA-256 of the current secret. The secret is 256 random bits, so no
    -- key derivation is needed, and the secret itself is never stored.
    secret_hash                      TEXT NOT NULL,
    -- Generation current_generation-1, still accepted until
    -- previous_expires_at. Both NULL when there is no previous secret.
    previous_secret_hash             TEXT NULL,
    previous_expires_at              DATETIME NULL,
    name                             TEXT NOT NULL,
    description                      TEXT NOT NULL DEFAULT '',
    -- The users.id of whoever created the key.
    created_by                       TEXT NOT NULL REFERENCES users(id),
    -- The optional group whose members manage this key; empty for none.
    -- Named group_name rather than group because GROUP is a reserved word
    -- in both dialects, as ldap_sync_runs.trigger_source avoids TRIGGER.
    group_name                       TEXT NOT NULL DEFAULT '',
    -- NULL when nss.key_max_lifetime is 0 and the creator chose no
    -- date: the key never expires. Fixed once the key exists.
    expires_at                       DATETIME NULL,
    notification_email               TEXT NOT NULL DEFAULT '',
    revoked_at                       DATETIME NULL,
    -- The users.id of whoever revoked the key.
    revoked_by                       TEXT NULL REFERENCES users(id),
    -- The last rotation, where mail.rotation_quiet_period starts.
    rotated_at                       DATETIME NULL,
    -- Notification claims, each taken with a guarded UPDATE so every
    -- instance can run every job without any of them sending twice; the
    -- same roles as enrollments.expiry_reminder_sent_at and
    -- enrollments.last_expired_attempt_notified_at. The first is the
    -- expiring reminder, the second the expired and revoked attempt mails,
    -- the third the daily rotation cleanup digest. NULL means never sent.
    expiry_reminder_sent_at          DATETIME NULL,
    last_expired_attempt_notified_at DATETIME NULL,
    rotation_cleanup_sent_at         DATETIME NULL,
    created_at                       DATETIME NOT NULL,
    updated_at                       DATETIME NOT NULL
);

-- Lookups per key, source address, generation and day, for the usage and
-- rotation reports. Written by a flusher that aggregates in memory, never
-- once per lookup, and pruned at nss.usage_retention.
CREATE TABLE nss_key_usage (
    key_id     TEXT NOT NULL REFERENCES nss_keys(id),
    -- The connection's address as the server observed it.
    source_ip  TEXT NOT NULL,
    -- source_ip as its 16-byte IPv6 form, an IPv4 address IPv4-mapped, so
    -- the admin key list's address search is an equality and a range
    -- search a byte range (both dialects compare these bytewise). NULL
    -- when source_ip does not parse as an address.
    source_ip_key BLOB NULL,
    -- The generation the host presented: current or current-1.
    generation INTEGER NOT NULL,
    -- Midnight UTC of the day the counts belong to.
    day        DATETIME NOT NULL,
    lookups    INTEGER NOT NULL DEFAULT 0,
    misses     INTEGER NOT NULL DEFAULT 0,
    first_seen DATETIME NOT NULL,
    last_seen  DATETIME NOT NULL,
    PRIMARY KEY (key_id, source_ip, generation, day)
);

-- One key's usage over a range of days, which is what both reports read.
CREATE INDEX idx_nss_key_usage_key_day ON nss_key_usage (key_id, day);
-- The retention prune, by age alone.
CREATE INDEX idx_nss_key_usage_day ON nss_key_usage (day);
-- The admin key list's address search: which keys were used from one
-- address, or from a range, within the retained days.
CREATE INDEX idx_nss_key_usage_source_day ON nss_key_usage (source_ip_key, day);

-- When each NSS alert was last raised, so a "once a day per uid" alert is
-- raised once a day across every instance: the raiser claims the row with a
-- guarded UPDATE (or the insert) and writes the audit event only when it
-- won. Rows are claim markers, not audit records, and may be deleted
-- freely; a deleted row only lets the next alert of its kind through early.
CREATE TABLE nss_alerts (
    -- uid_collision, name_collision, id_change_pending, name_mismatch,
    -- privileged_name, key_source and the like.
    kind           TEXT NOT NULL,
    -- What the alert is about: a uid, a name, a row id or a key id.
    subject        TEXT NOT NULL,
    last_raised_at DATETIME NOT NULL,
    PRIMARY KEY (kind, subject)
);

-- Which stored accounts and groups each NSS audit event concerns, so the
-- viewer's history of one row reads an index rather than matching the
-- events' JSON (decided 2026-10-18 by the owner). Written in the
-- transaction that writes the event. A grouping key, never authoritative,
-- as audit_events.target_user_id is: the payload is the record.
CREATE TABLE nss_audit_refs (
    -- Deleted with its event, which only the audit sweep deletes.
    event_id   TEXT NOT NULL REFERENCES audit_events(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('account', 'group')),
    -- A posix_users.id for an account, a posix_groups.id for a group: the
    -- random id, which survives a rename and an accepted uid or gid change
    -- where the name and the number do not. No foreign key, so a purged
    -- row's history stays until its events age out.
    row_id     TEXT NOT NULL,
    -- The event's created_at, so a page orders and seeks on this table
    -- alone.
    created_at DATETIME NOT NULL,
    PRIMARY KEY (event_id, kind, row_id)
);

-- One row's history, newest first, keyset paged on (created_at, event_id)
-- as the per-user audit timeline is on (created_at, id). The primary key
-- leads with event_id, which serves the cascade from the sweep.
CREATE INDEX idx_nss_audit_refs_row ON nss_audit_refs (kind, row_id, created_at, event_id);
