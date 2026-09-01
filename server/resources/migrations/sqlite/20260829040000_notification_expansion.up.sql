-- Expanded notification catalogue: a per-enrollment notification address,
-- and the two send-once claims the enrollment-scoped kinds need.
-- See docs/proposals/notification-kinds-expansion.md.

-- The address a notification about this enrollment goes to instead of
-- fanning out to every holder of its service account.
--
-- NOT NULL DEFAULT '' rather than nullable, matching service_account added
-- alongside it: an email address is never legitimately empty, so '' is an
-- unambiguous "unset" that needs no three-valued logic in the delivery
-- branch, and existing rows need no backfill.
ALTER TABLE enrollments ADD COLUMN notification_email TEXT NOT NULL DEFAULT '';

-- The expiry reminder's send-once claim. Every instance runs the sweep and
-- the queue group deduplicates consumption rather than publication, so the
-- claim has to live here: the sweep takes it with a guarded UPDATE and
-- publishes only when that reports one row.
--
-- Nullable on purpose -- IS NULL is the claim -- and deliberately not
-- backfilled: an enrollment already inside the reminder window when this
-- migration runs gets its one reminder on the next sweep, which is the
-- outcome the feature exists for.
ALTER TABLE enrollments ADD COLUMN expiry_reminder_sent_at DATETIME;

-- The expired-attempt notification's rate-limit claim. A broken cron job
-- retries an expired code forever, so this one is a window rather than a
-- one-shot: the guarded UPDATE also matches a row whose timestamp is older
-- than the window, letting the next attempt after it claim again.
ALTER TABLE enrollments ADD COLUMN last_expired_attempt_notified_at DATETIME;

-- The reminder sweep's query: unexpired enrollments inside the lead window
-- with no reminder claimed. expires_at alone is not enough of a filter --
-- every enrollment ever created has one, and all but a few have already
-- been reminded.
CREATE INDEX idx_enrollments_expiry_reminder ON enrollments(expiry_reminder_sent_at, expires_at);
