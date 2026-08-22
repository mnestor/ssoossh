-- Downgrade: drop all tables to revert to pre-migrated state.
-- This is a full schema reset since there is only one migration.
-- See .claude/rules/database.md: "Always include rollback instructions."

DROP TABLE IF EXISTS server_secrets;
DROP TABLE IF EXISTS host_mappings;
DROP TABLE IF EXISTS enrollments;
DROP TABLE IF EXISTS certificate_request_decisions;
DROP TABLE IF EXISTS certificates;
DROP TABLE IF EXISTS certificate_requests;
DROP TABLE IF EXISTS users;
