-- Downgrade: drop both additions. The previous release read neither, and the
-- directory data they annotate is untouched — a re-anchoring ID is rebuilt on
-- the next sync pass, and a display name on the next login.
DROP INDEX IF EXISTS idx_user_ldap_directory_id;
ALTER TABLE user_ldap DROP COLUMN directory_id;
ALTER TABLE users DROP COLUMN display_name;
