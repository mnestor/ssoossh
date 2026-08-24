-- Downgrade: drop the index. Nothing depends on it for correctness -- the
-- join it serves still resolves without it, only slower.

DROP INDEX IF EXISTS idx_enrollment_retrievals_certificate_serial;
