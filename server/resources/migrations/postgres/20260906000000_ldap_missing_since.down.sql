-- Downgrade: drop the first-missing timestamp. The previous release counted
-- passes instead, and consecutive_misses was never stopped being written, so
-- it is still there for that release to read.

ALTER TABLE user_ldap DROP COLUMN first_missing_at;
