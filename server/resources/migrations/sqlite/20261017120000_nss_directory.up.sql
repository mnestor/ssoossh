-- The NSS directory: the POSIX accounts and groups served to hosts over
-- /api/nss/*, the fleet keys those hosts authenticate with, the keys' usage
-- aggregate, and the claim markers that hold each NSS alert to one per
-- window across instances. See docs/proposals/nss-directory/1-server-api.md
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
    -- The entry's DN from the last successful read.
    dn              TEXT NOT NULL,
    -- The person, set at login when the LDAP enrichment resolves this
    -- entry. NULL for shared and service accounts and for anyone who has
    -- not logged in. Nulled rather than deleted with the user, because the
    -- account belongs to the directory, not to the login.
    user_id         TEXT NULL REFERENCES users(id) ON DELETE SET NULL,
    -- The mapped primary gid, frozen like uid. auto_private_groups is
    -- applied when the row is read, never stored here, so a config change
    -- is deliberate and a directory change is caught.
    gid             INTEGER NOT NULL,
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
    -- The person's own choice from nss.allowed_shells. Only meaningful on a
    -- row with a user_id.
    shell_pref      TEXT NULL,
    -- The rendered and sanitised nss.gecos_template.
    gecos           TEXT NOT NULL DEFAULT '',
    -- The directory's own disabled flag, from nss.disabled_filter.
    dir_disabled    INTEGER NOT NULL DEFAULT 0,
    -- The first sync that could not find the entry by id, DN or filter.
    -- NULL while the entry resolves.
    missing_since   DATETIME NULL,
    -- Set once missing_since is older than nss.tombstone_after, cleared if
    -- the entry reappears. A tombstone still holds its uid and name, so
    -- neither can be handed on until nss.tombstone_retention purges the row
    -- or an admin does.
    deleted_at      DATETIME NULL,
    -- Set while the row is part of a uid or name collision, cleared when
    -- the collision resolves. Lookups treat a withheld row as not found.
    -- Row state rather than state of one sync run, so every instance, and
    -- one that has just restarted, withholds the same rows. uid and name
    -- are unique, so the row here is the holder of the contested value and
    -- withheld_reason names the other entry, in a short sentence for the
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
CREATE UNIQUE INDEX idx_posix_users_dn ON posix_users (dn);
-- The login link and the shell self-service read: at most one account per
-- person. NULLs are distinct in both dialects, so unlinked rows never
-- collide.
CREATE UNIQUE INDEX idx_posix_users_user_id ON posix_users (user_id);
-- Partial, as idx_user_ldap_directory_id: the empty string means "no ID"
-- and is not worth indexing.
CREATE INDEX idx_posix_users_directory_id ON posix_users (directory_id) WHERE directory_id <> '';

-- One row per directory group with the mapped gid attribute, plus the
-- synthetic private groups auto_private_groups creates.
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
    -- Empty string for a synthetic group, which has no entry.
    dn              TEXT NOT NULL DEFAULT '',
    synthetic       INTEGER NOT NULL DEFAULT 0,
    -- As posix_users: set while the row is part of a gid or name collision.
    withheld_at     DATETIME NULL,
    withheld_reason TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL
);

-- getgrgid and getgrnam, and one record per number and per name.
CREATE UNIQUE INDEX idx_posix_groups_gid ON posix_groups (gid);
CREATE UNIQUE INDEX idx_posix_groups_name ON posix_groups (name);
-- The sync's two anchors. Both partial: synthetic groups hold the empty
-- string in each.
CREATE INDEX idx_posix_groups_directory_id ON posix_groups (directory_id) WHERE directory_id <> '';
CREATE INDEX idx_posix_groups_dn ON posix_groups (dn) WHERE dn <> '';

-- Group membership by number. Keyed by uid rather than users.id because
-- members include accounts that never log in. No foreign keys: an accepted
-- uid or gid change re-keys these rows itself, in the same transaction that
-- changes the number, and a constraint would fix the order of those writes.
CREATE TABLE posix_group_members (
    gid INTEGER NOT NULL,
    uid INTEGER NOT NULL,
    PRIMARY KEY (gid, uid)
);

-- The primary key answers "members of this group"; this answers "groups of
-- this user", the initgroups lookup.
CREATE INDEX idx_posix_group_members_uid ON posix_group_members (uid);

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
    expires_at                       DATETIME NOT NULL,
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
