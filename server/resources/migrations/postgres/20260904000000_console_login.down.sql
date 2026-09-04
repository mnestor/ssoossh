-- Downgrade: drop the console columns and narrow both type CHECKs back to
-- the three types the previous release knows about.
--
-- Console requests and any certificate they produced are deleted rather
-- than rewritten to another type: the previous release has no code that can
-- read them, and relabelling a console session as a `sudo` would put a row
-- in the audit trail that says something that never happened. Certificates
-- go first so the requests they reference can follow.

DELETE FROM certificates WHERE type = 'console';
DELETE FROM certificate_requests WHERE type = 'console';

DROP INDEX IF EXISTS idx_certificate_requests_user_code;

ALTER TABLE certificate_requests DROP COLUMN user_code;
ALTER TABLE certificate_requests DROP COLUMN hostname;
ALTER TABLE certificate_requests DROP COLUMN pam_service;
ALTER TABLE certificate_requests DROP COLUMN tty;
ALTER TABLE certificate_requests DROP COLUMN remote_host;

ALTER TABLE certificates DROP CONSTRAINT chk_certificates_type;
ALTER TABLE certificates ADD CONSTRAINT chk_certificates_type
    CHECK (type IN ('user', 'service', 'pam'));

ALTER TABLE certificate_requests DROP CONSTRAINT chk_certificate_requests_type;
ALTER TABLE certificate_requests ADD CONSTRAINT chk_certificate_requests_type
    CHECK (type IN ('user', 'service', 'pam'));
