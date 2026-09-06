-- Two stable identifiers and one human-readable name.
--
-- users.display_name is the person's name as a person reads it — "Ada
-- Lovelace" rather than "alovelace". Display only: it is shown beside the
-- username in the web UI and offered to email templates, and it is
-- deliberately not a certificate principal, not a key ID input, and never
-- an authorization input. Captured from authentication.fields.name, and
-- overridden by ldap.fields.name where the directory has the better copy.
--
-- Empty string rather than NULL, because every reader wants a string and
-- "no name captured" and "name is the empty string" are the same answer.
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

-- user_ldap.directory_id is the entry's unique, immutable identifier
-- (entryUUID, objectGUID, ipaUniqueID — named by ldap.id_attribute), and it
-- is what makes a directory rename survivable.
--
-- Resolution without it is DN first, then the rendered user_filter. Both
-- move: a DN changes when someone is moved between OUs, and a user_filter
-- keyed on {{.Username}} stops matching the moment they are renamed. A
-- rename therefore looked exactly like a deletion and walked the account
-- toward the sync.disable_after auto-disable. With an ID stored, the sync
-- searches by it first and re-anchors the DN instead.
--
-- Empty string on existing rows and on deployments that leave
-- ldap.id_attribute unset; those keep the DN-then-filter path unchanged.
ALTER TABLE user_ldap ADD COLUMN directory_id TEXT NOT NULL DEFAULT '';

-- Partial, because the empty string is the common value and indexing it
-- would be indexing "no ID". The lookup this serves is by a specific ID.
CREATE INDEX idx_user_ldap_directory_id ON user_ldap (directory_id) WHERE directory_id <> '';
