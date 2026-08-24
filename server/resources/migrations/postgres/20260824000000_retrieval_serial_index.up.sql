-- A service certificate is tied to the redemption that produced it by
-- serial, not by a foreign key: EnrollmentService.Retrieve pre-allocates the
-- serial onto the enrollment_retrievals row before queueing the signing job,
-- and the same value lands on the certificates row the signed reply writes.
--
-- The certificate-history query walks that join for every certificate it
-- returns, so the retrieval side needs an index of its own. The existing
-- index covers enrollment_id, which this lookup does not have -- finding the
-- retrieval is the whole point of it.
CREATE INDEX IF NOT EXISTS idx_enrollment_retrievals_certificate_serial
    ON enrollment_retrievals(certificate_serial);
