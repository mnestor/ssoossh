-- Downgrade: drop all tables to revert to pre-migrated state.
-- This is a full schema reset since there is only one migration.
-- See .claude/rules/database.md: "Always include rollback instructions."
--
-- Reverse creation order, so nothing is dropped while a surviving table
-- still REFERENCEs it.

DROP TABLE IF EXISTS server_secrets;
DROP TABLE IF EXISTS ca_signer_keys;
DROP TABLE IF EXISTS notification_preferences;
DROP TABLE IF EXISTS enrollment_reassignments;
DROP TABLE IF EXISTS enrollment_retrievals;
DROP TABLE IF EXISTS enrollments;
DROP TABLE IF EXISTS certificate_request_decisions;
DROP TABLE IF EXISTS certificates;
DROP TABLE IF EXISTS certificate_requests;
DROP TABLE IF EXISTS users;
