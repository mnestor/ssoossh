-- Group ownership of service enrollments. An enrollment is owned by every
-- user holding its service account, not by the single user who approved it.
-- See docs/proposals/enrollment-group-ownership.md.

-- The enrollment's service account, denormalized out of the principals JSON
-- array it has always been the sole element of (see
-- CertRequestService.approveServiceEnrollment). Ownership is now a query --
-- "every enrollment for the accounts I hold" -- and a query cannot reach
-- into a JSON string portably across both dialects.
--
-- NOT NULL DEFAULT '' rather than nullable: '' is the honest value for a row
-- whose principals never parsed, and it matches no service account, so such
-- a row is owned by nobody while staying visible to auditors.
ALTER TABLE enrollments ADD COLUMN service_account TEXT NOT NULL DEFAULT '';

-- Backfill from principals[0], by pattern rather than by JSON cast:
-- principals::json raises on a malformed row and would fail the whole
-- migration, and Postgres has no try-cast to guard it with (IS JSON needs
-- 16+, below the versions this supports). substring returns NULL on no
-- match, so a row that never parsed simply keeps ''.
--
-- The pattern is exact, not a heuristic: a principal is [a-zA-Z0-9._-]+
-- (internal/crypto/ssh.ValidatePrincipal), so the encoding json.Marshal
-- produces is always literally ["name"] with nothing to escape.
UPDATE enrollments
SET service_account = COALESCE(substring(principals from '^\["([a-zA-Z0-9._-]+)"'), '')
WHERE principals <> '';

-- The ownership query: every enrollment for a set of service accounts.
CREATE INDEX idx_enrollments_service_account ON enrollments(service_account);
