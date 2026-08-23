-- Host certificates are removed (docs/decisions.md): no secure way exists
-- today to verify a host's claim to a hostname, so nothing may issue or
-- store host certificates until something like an ACME challenge provides
-- one. Host rows are deleted rather than kept: the signer never issued a
-- host certificate (it rejected the type), so these are requests that
-- could never complete.
--
-- SQLite cannot alter a CHECK constraint in place, so both tables are
-- rebuilt with the standard recreate pattern (copy, drop, rename,
-- recreate indexes) under PRAGMA foreign_keys=OFF — the same pattern the
-- init migration anticipates.
PRAGMA foreign_keys=OFF;

DELETE FROM certificates WHERE type = 'host';
DELETE FROM certificate_request_decisions WHERE certificate_request_id IN
    (SELECT id FROM certificate_requests WHERE type = 'host');
DELETE FROM certificate_requests WHERE type = 'host';

CREATE TABLE certificate_requests_new (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL
        CONSTRAINT chk_certificate_requests_type
        CHECK (type IN ('user', 'service', 'pam')),
    user_id TEXT REFERENCES users(id),
    public_key TEXT NOT NULL,
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
        CHECK (type IN ('user', 'service', 'pam')),
    user_id TEXT REFERENCES users(id),
    certificate_request_id TEXT REFERENCES certificate_requests(id),
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

DROP TABLE host_mappings;

PRAGMA foreign_keys=ON;
