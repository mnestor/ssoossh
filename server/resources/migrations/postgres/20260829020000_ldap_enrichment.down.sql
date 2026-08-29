-- Downgrade: drop directory sync state and persisted groups. Identity
-- itself is untouched; OIDC claims alone remain sufficient, which is the
-- posture LDAP enrichment is designed around.

DROP INDEX IF EXISTS idx_user_groups_name;
DROP INDEX IF EXISTS idx_user_groups_user;
DROP INDEX IF EXISTS idx_user_groups_unique;
DROP TABLE IF EXISTS user_groups;
DROP TABLE IF EXISTS user_ldap;

ALTER TABLE users DROP COLUMN disabled_source;
