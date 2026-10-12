-- Rollback: drops the action column and its indexes. Nothing is lost: the
-- payload still carries every action. The indexes go first, since SQLite
-- will not drop an indexed column.
DROP INDEX idx_audit_events_action_actor;
DROP INDEX idx_audit_events_action;
ALTER TABLE audit_events DROP COLUMN action;
