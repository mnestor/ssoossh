-- Downgrade: drop the audit stream and the denormalized disable reason.
-- The shipped type=audit log is unaffected; it is the archive.

DROP INDEX IF EXISTS idx_audit_events_created_at;
DROP INDEX IF EXISTS idx_audit_events_target;
DROP INDEX IF EXISTS idx_audit_events_actor;
DROP TABLE IF EXISTS audit_events;

ALTER TABLE users DROP COLUMN disabled_reason;
