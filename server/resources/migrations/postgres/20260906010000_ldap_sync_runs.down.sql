-- Downgrade: drop the run history. The previous release reported passes to
-- the log only, which is unaffected.

DROP TABLE ldap_sync_runs;
