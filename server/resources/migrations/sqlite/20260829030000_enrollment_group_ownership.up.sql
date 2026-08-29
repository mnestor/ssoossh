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

-- Backfill from principals[0]. json_valid guards the rows written before
-- principals were fixed at approval time: json_extract raises on malformed
-- input rather than returning NULL, which would fail the migration rather
-- than skip the row. COALESCE covers a well-formed array whose first element
-- is JSON null, which NOT NULL would otherwise reject.
UPDATE enrollments
SET service_account = COALESCE(json_extract(principals, '$[0]'), '')
WHERE json_valid(principals)
  AND json_array_length(principals) > 0;

-- The ownership query: every enrollment for a set of service accounts.
CREATE INDEX idx_enrollments_service_account ON enrollments(service_account);
