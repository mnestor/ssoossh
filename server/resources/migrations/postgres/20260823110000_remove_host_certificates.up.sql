-- Host certificates are removed (docs/decisions.md): no secure way exists
-- today to verify a host's claim to a hostname, so nothing may issue or
-- store host certificates until something like an ACME challenge provides
-- one. Host rows are deleted rather than kept: the signer never issued a
-- host certificate (it rejected the type), so these are requests that
-- could never complete.
DELETE FROM certificates WHERE type = 'host';
DELETE FROM certificate_request_decisions WHERE certificate_request_id IN
    (SELECT id FROM certificate_requests WHERE type = 'host');
DELETE FROM certificate_requests WHERE type = 'host';

ALTER TABLE certificate_requests DROP COLUMN hostname;
ALTER TABLE certificate_requests DROP CONSTRAINT chk_certificate_requests_type;
ALTER TABLE certificate_requests ADD CONSTRAINT chk_certificate_requests_type
    CHECK (type IN ('user', 'service', 'pam'));

ALTER TABLE certificates DROP COLUMN hostname;
ALTER TABLE certificates DROP CONSTRAINT chk_certificates_type;
ALTER TABLE certificates ADD CONSTRAINT chk_certificates_type
    CHECK (type IN ('user', 'service', 'pam'));

DROP TABLE host_mappings;
