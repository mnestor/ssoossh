CREATE TABLE host_mappings (
    id TEXT PRIMARY KEY NOT NULL,
    hostname TEXT NOT NULL,
    principals TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX idx_host_mappings_hostname ON host_mappings(hostname);

ALTER TABLE certificates ADD COLUMN hostname TEXT NOT NULL DEFAULT '';
ALTER TABLE certificates DROP CONSTRAINT chk_certificates_type;
ALTER TABLE certificates ADD CONSTRAINT chk_certificates_type
    CHECK (type IN ('user', 'host', 'service', 'pam'));

ALTER TABLE certificate_requests ADD COLUMN hostname TEXT NOT NULL DEFAULT '';
ALTER TABLE certificate_requests DROP CONSTRAINT chk_certificate_requests_type;
ALTER TABLE certificate_requests ADD CONSTRAINT chk_certificate_requests_type
    CHECK (type IN ('user', 'host', 'service', 'pam'));
