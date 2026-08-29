-- Downgrade: drop the denormalized service account. principals still holds
-- it, so nothing is lost; ownership reverts to enrollments.user_id.

DROP INDEX IF EXISTS idx_enrollments_service_account;

ALTER TABLE enrollments DROP COLUMN service_account;
