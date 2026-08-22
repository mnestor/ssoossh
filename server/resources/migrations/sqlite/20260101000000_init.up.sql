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
    username TEXT NOT NULL DEFAULT '',
    requested_options TEXT NOT NULL DEFAULT '',
    source_ip TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    resolved_at DATETIME,
    enrollment_token TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    -- Local OS identity of the client making a user-type certificate
    -- request — the local client is the requester for that type, so this
    -- is who/where the request actually came from. Populated client-side
    -- (os/user.Current(), os.Hostname()) for CertificateTypeUser requests
    -- only; empty for every other type. See
    -- docs/certificate-audit-metadata-plan.md.
    local_username TEXT NOT NULL DEFAULT '',
    local_hostname TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_certificate_requests_status ON certificate_requests(status);

-- The audit record of a single Approve/Deny decision. One row per decision,
-- ever, inserted once and never updated or deleted — see
-- server/model/certificate_request_decision.go and
-- docs/certificate-audit-metadata-plan.md. Kept in its own table rather than
-- as columns on certificate_requests: that table is the busy, read/write
-- pipeline table (status transitions, the sweep); this is an append-only
-- log entry about one event in that pipeline's history, and its own table
-- means new indexed columns can be added here later without ever touching
-- the pipeline table.
--
-- Scalars each get their own column so they're individually indexable; the
-- three list-valued identity fields (groups, other_accounts,
-- service_accounts) are JSON-encoded TEXT, matching this project's existing
-- JSON-in-TEXT-not-dialect-JSON convention for requested_options.
--
-- The UNIQUE constraint on certificate_request_id enforces "at most one
-- decision per request" at the database level, as defense in depth: the
-- guarded UPDATE ... WHERE status = 'pending' in
-- CertRequestService.Approve/Deny already ensures only one caller ever wins
-- the race to resolve a given request, so this should never be exercised in
-- normal operation.
CREATE TABLE certificate_request_decisions (
    id TEXT PRIMARY KEY,
    certificate_request_id TEXT NOT NULL UNIQUE REFERENCES certificate_requests(id),
    outcome TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    groups TEXT NOT NULL DEFAULT '',
    other_accounts TEXT NOT NULL DEFAULT '',
    service_accounts TEXT NOT NULL DEFAULT '',
    source_ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    accept_language TEXT NOT NULL DEFAULT '',
    forwarded_for TEXT NOT NULL DEFAULT '',
    decided_at DATETIME NOT NULL
);

-- "Everything this person decided" — a common audit question, and without
-- this index it's a full table scan.
CREATE INDEX idx_certificate_request_decisions_subject ON certificate_request_decisions(subject);

-- "Every approval not from the office range" — the source-network signal
-- called out in docs/ssoossh-context.md's lifetime-policy section.
CREATE INDEX idx_certificate_request_decisions_source_ip ON certificate_request_decisions(source_ip);

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

CREATE TABLE server_secrets (
    name TEXT PRIMARY KEY,
    value BLOB NOT NULL,
    created_at DATETIME NOT NULL
);

COMMIT;
PRAGMA foreign_keys=ON;
