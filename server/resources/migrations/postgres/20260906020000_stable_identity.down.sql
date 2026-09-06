DROP INDEX IF EXISTS idx_user_ldap_directory_id;
ALTER TABLE user_ldap DROP COLUMN directory_id;
ALTER TABLE users DROP COLUMN display_name;
