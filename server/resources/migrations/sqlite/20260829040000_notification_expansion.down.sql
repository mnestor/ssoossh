-- Downgrade: drop the notification address and the two send-once claims.
--
-- The claims are bookkeeping, so losing them costs at most one duplicate
-- reminder on a re-upgrade. The address is real configuration and is gone
-- for good: enrollments revert to fanning out to every account holder.

DROP INDEX IF EXISTS idx_enrollments_expiry_reminder;

ALTER TABLE enrollments DROP COLUMN last_expired_attempt_notified_at;
ALTER TABLE enrollments DROP COLUMN expiry_reminder_sent_at;
ALTER TABLE enrollments DROP COLUMN notification_email;
