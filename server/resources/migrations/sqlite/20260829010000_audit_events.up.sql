-- The append-only administrative audit stream. See
-- docs/proposals/audit-log.md and model.AuditEvent.
--
-- No foreign keys, by design: an audit entry must read the same in five
-- years as it did the day it was written, so identity is copied into the
-- payload as a snapshot rather than referenced. The two user-id columns are
-- indexed grouping keys for the UI's two timelines ("everything this
-- account did" and "everything done to this account"), never references and
-- never authoritative.
--
-- This table is a bounded cache pruned on a schedule; the shipped
-- type=audit log is the archive.

CREATE TABLE audit_events (
    id             TEXT PRIMARY KEY,
    created_at     DATETIME NOT NULL,
    actor_user_id  TEXT NULL,
    target_user_id TEXT NULL,
    payload        TEXT NOT NULL DEFAULT ''
);

-- Both timelines are "this user, newest first", so the sort column is part
-- of each index rather than a separate sort step.
CREATE INDEX idx_audit_events_actor ON audit_events (actor_user_id, created_at);
CREATE INDEX idx_audit_events_target ON audit_events (target_user_id, created_at);

-- The recent-activity feed and the retention sweep both order by age alone.
CREATE INDEX idx_audit_events_created_at ON audit_events (created_at);

-- Why a user was disabled, so the next admin deciding whether to re-enable
-- can see it without reading the audit trail. The audit trail is the
-- history; this column is the current state, and it survives audit pruning.
ALTER TABLE users ADD COLUMN disabled_reason TEXT NOT NULL DEFAULT '';
