-- Downgrade: drop the group id. The previous release never read it.

ALTER TABLE certificate_requests DROP COLUMN caller_gid;
