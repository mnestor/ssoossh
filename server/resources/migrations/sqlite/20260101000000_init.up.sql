PRAGMA foreign_keys=OFF;
BEGIN;

-- See server/model for the corresponding GORM structs; a column added here
-- must also be added to postgres/20260101000000_init.up.sql and model/.

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    username TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    other_accounts TEXT NOT NULL DEFAULT '',
    service_accounts TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX idx_users_subject ON users(subject);

CREATE TABLE certificates (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    user_id TEXT REFERENCES users(id),
    hostname TEXT NOT NULL DEFAULT '',
    public_key_fingerprint TEXT NOT NULL,
    serial_number INTEGER NOT NULL,
    key_id TEXT NOT NULL DEFAULT '',
    principals TEXT NOT NULL DEFAULT '',
    critical_options TEXT NOT NULL DEFAULT '',
    extensions TEXT NOT NULL DEFAULT '',
    issued_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL
);
CREATE INDEX idx_certificates_user_id ON certificates(user_id);

CREATE TABLE certificate_requests (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    user_id TEXT REFERENCES users(id),
    public_key TEXT NOT NULL,
    hostname TEXT NOT NULL DEFAULT '',
    requested_options TEXT NOT NULL DEFAULT '',
    source_ip TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    resolved_at DATETIME,
    enrollment_token TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_certificate_requests_status ON certificate_requests(status);

CREATE TABLE enrollments (
    id TEXT PRIMARY KEY,
    code TEXT NOT NULL,
    public_key TEXT NOT NULL,
    option_set TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL,
    redeemed_at DATETIME
);
CREATE UNIQUE INDEX idx_enrollments_code ON enrollments(code);

CREATE TABLE host_mappings (
    id TEXT PRIMARY KEY,
    hostname TEXT NOT NULL,
    principals TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX idx_host_mappings_hostname ON host_mappings(hostname);

COMMIT;
PRAGMA foreign_keys=ON;
