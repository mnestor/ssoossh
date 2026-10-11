-- Clears disabled_source where the admin disable and re-enable left a wrong
-- one. Neither handler used to write the column, so a re-enable left
-- ldap_sync behind on an account the directory sync had disabled, and a
-- later admin or SOC disable of that account kept it. The sync clears any
-- disable whose source is ldap_sync, so it would undo that operator's disable
-- the next time the directory entry resolved.
--
-- Two kinds of row carry a stale ldap_sync:
--   * an enabled account (disabled_at IS NULL), where it would mislabel the
--     next operator disable;
--   * a disabled account with disabled_by_user_id set, which only an operator
--     disable writes. The sync's own disable leaves it NULL.
-- Both become NULL rather than a guess: whether the operator acted as admin
-- or SOC is recorded nowhere else, and NULL is a source the sync never clears.
UPDATE users
SET disabled_source = NULL
WHERE disabled_source = 'ldap_sync'
  AND (disabled_at IS NULL OR disabled_by_user_id IS NOT NULL);
