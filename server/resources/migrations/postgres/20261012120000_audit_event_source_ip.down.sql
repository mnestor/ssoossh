-- Rollback: drops the source_ip column and its indexes. Nothing is lost: the
-- payload still carries every source address.
DROP INDEX idx_audit_events_action_source_ip;
DROP INDEX idx_audit_events_source_ip;
ALTER TABLE audit_events DROP COLUMN source_ip;
