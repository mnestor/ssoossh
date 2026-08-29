-- LDAP enrichment, directory sync, and the first persisted group storage.
-- See docs/operations/ldap.md and docs/proposals/ldap-enrichment-and-sync.md.

-- Sync bookkeeping, one row per user who has logged in while LDAP was
-- enabled. Only known users sync: the server never enumerates the
-- directory, which keeps the user set self-selecting.
CREATE TABLE user_ldap (
    user_id            TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    dn                 TEXT NOT NULL DEFAULT '',
    attributes         TEXT NOT NULL DEFAULT '',
    last_seen_at       TIMESTAMPTZ NULL,
    last_synced_at     TIMESTAMPTZ NULL,
    consecutive_misses INTEGER NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL
);

-- Persisted group membership, for notification fan-out and display. Never
-- an authorization input: authorization reads the session identity only
-- (see docs/internals/invariants.md).
--
-- Rows rather than JSON so "everyone in soc" is one indexed query. Unique
-- per (user, group, source) so the two capture paths never collide and
-- either can be replaced without touching the other.
CREATE TABLE user_groups (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_name    TEXT NOT NULL,
    source        TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at  TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_user_groups_source CHECK (source IN ('oidc','ldap'))
);

CREATE UNIQUE INDEX idx_user_groups_unique ON user_groups (user_id, group_name, source);
CREATE INDEX idx_user_groups_user ON user_groups (user_id);
-- The fan-out query: everyone in a named group.
CREATE INDEX idx_user_groups_name ON user_groups (group_name);

-- What disabled a user, which is what makes auto-re-enable safe: the sync
-- clears only disables whose source is exactly ldap_sync, so an admin or
-- SOC disable is never undone automatically.
--
-- Nullable with no backfill. Users disabled before this migration carry
-- NULL, which the sync's exact-match rule can never touch — the safe
-- direction. Writing 'admin' into old rows would behave identically, so
-- the migration takes the cheaper path.
ALTER TABLE users ADD COLUMN disabled_source TEXT NULL;
