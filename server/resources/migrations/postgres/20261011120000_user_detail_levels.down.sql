-- Rollback: drops both detail level columns. Every stored choice is lost;
-- the web UI falls back to its browser-only setting on the build before.
ALTER TABLE users DROP COLUMN email_detail_level;
ALTER TABLE users DROP COLUMN web_detail_level;
