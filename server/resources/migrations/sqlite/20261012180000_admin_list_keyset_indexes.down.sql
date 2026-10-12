-- Rollback: drops the admin list keyset indexes. Nothing is lost; the lists
-- still page correctly, only by sorting the table.
DROP INDEX idx_enrollments_created_at_id;
DROP INDEX idx_certificates_issued_at_id;
