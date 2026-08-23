-- Restore the pre-removal shape: hostname columns back (empty), 'host'
-- back in the type CHECKs, host_mappings table back. Deleted host rows are
-- not recoverable.
PRAGMA foreign_keys=OFF;

CREATE TABLE certificate_requests_new (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL
        CONSTRAINT chk_certificate_requests_type
        CHECK (type IN ('user', 'host', 'service', 'pam')),
    user_id TEXT REFERENCES users(id),
    public_key TEXT NOT NULL,
    hostname TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    requested_options TEXT NOT NULL DEFAULT '',
    source_ip TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL
        CONSTRAINT chk_certificate_requests_status
        CHECK (status IN ('pending', 'signing', 'approved', 'enrolled', 'denied', 'expired', 'failed')),
    created_at DATETIME NOT NULL,
    resolved_at DATETIME,
    enrollment_token TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    local_username TEXT NOT NULL DEFAULT '',
    local_hostname TEXT NOT NULL DEFAULT '',
    service_account TEXT NOT NULL DEFAULT '',
    serial_number INTEGER
);
INSERT INTO certificate_requests_new
    (id, type, user_id, public_key, username, requested_options, source_ip,
     status, created_at, resolved_at, enrollment_token, failure_reason,
     local_username, local_hostname, service_account, serial_number)
SELECT id, type, user_id, public_key, username, requested_options, source_ip,
     status, created_at, resolved_at, enrollment_token, failure_reason,
     local_username, local_hostname, service_account, serial_number
FROM certificate_requests;
DROP TABLE certificate_requests;
ALTER TABLE certificate_requests_new RENAME TO certificate_requests;
CREATE INDEX idx_certificate_requests_status_created_at ON certificate_requests(status, created_at);
CREATE INDEX idx_certificate_requests_user_id ON certificate_requests(user_id);

CREATE TABLE certificates_new (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL
        CONSTRAINT chk_certificates_type
        CHECK (type IN ('user', 'host', 'service', 'pam')),
    user_id TEXT REFERENCES users(id),
    certificate_request_id TEXT REFERENCES certificate_requests(id),
    hostname TEXT NOT NULL DEFAULT '',
    public_key_fingerprint TEXT NOT NULL,
    serial_number INTEGER NOT NULL UNIQUE,
    key_id TEXT NOT NULL DEFAULT '',
    principals TEXT NOT NULL DEFAULT '',
    critical_options TEXT NOT NULL DEFAULT '',
    extensions TEXT NOT NULL DEFAULT '',
    issued_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL
);
INSERT INTO certificates_new
    (id, type, user_id, certificate_request_id, public_key_fingerprint,
     serial_number, key_id, principals, critical_options, extensions,
     issued_at, expires_at)
SELECT id, type, user_id, certificate_request_id, public_key_fingerprint,
     serial_number, key_id, principals, critical_options, extensions,
     issued_at, expires_at
FROM certificates;
DROP TABLE certificates;
ALTER TABLE certificates_new RENAME TO certificates;
CREATE INDEX idx_certificates_user_id_issued_at ON certificates(user_id, issued_at DESC);
CREATE INDEX idx_certificates_certificate_request_id ON certificates(certificate_request_id);

CREATE TABLE host_mappings (
    id TEXT PRIMARY KEY NOT NULL,
    hostname TEXT NOT NULL,
    principals TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX idx_host_mappings_hostname ON host_mappings(hostname);

PRAGMA foreign_keys=ON;
