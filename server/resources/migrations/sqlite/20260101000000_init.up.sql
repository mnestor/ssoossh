PRAGMA foreign_keys=OFF;
BEGIN;

-- See server/model for the corresponding GORM structs; a column added here
-- must also be added to postgres/20260101000000_init.up.sql and model/.
--
-- Table order matters: a table must be created before anything REFERENCEs
-- it. certificate_requests therefore precedes certificates, which carries a
-- certificate_request_id foreign key back to it.
--
-- CHECK constraints on the enum-valued columns (type, status, outcome) are
-- mirrored as `check:` tags on the model structs, so the AutoMigrate-backed
-- unit tests build the same constraint the migration does and can exercise
-- it directly. Adding an enum value therefore means editing three places:
-- server/model/enums.go, the model's `check:` tag, and both migrations.

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

CREATE TABLE certificate_requests (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL
        CONSTRAINT chk_certificate_requests_type
        CHECK (type IN ('user', 'host', 'service', 'pam')),
    user_id TEXT REFERENCES users(id),
    public_key TEXT NOT NULL,
    hostname TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    requested_options TEXT NOT NULL DEFAULT '',
    source_ip TEXT NOT NULL DEFAULT '',
    -- Every status transition is a guarded UPDATE ... WHERE status = ?, so a
    -- value outside this set would strand the row: no guarded update would
    -- ever match it again and the sweep would never see it. The CHECK turns
    -- that into a failed write instead of a silently unreachable request.
    status TEXT NOT NULL
        CONSTRAINT chk_certificate_requests_status
        CHECK (status IN ('pending', 'signing', 'approved', 'enrolled', 'denied', 'expired', 'failed')),
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

-- The sweep is the only query that filters on status alone, and it pairs it
-- with a created_at range (service.SweepStrandedRequests). Every other
-- status predicate is `id = ? AND status = ?`, a primary-key lookup that
-- doesn't use this index at all, so the range column earns its place here.
CREATE INDEX idx_certificate_requests_status_created_at ON certificate_requests(status, created_at);

-- Declared foreign key, so it gets an index: without one, any pre-delete
-- check or ON DELETE action on users degrades to a full scan.
CREATE INDEX idx_certificate_requests_user_id ON certificate_requests(user_id);

CREATE TABLE certificates (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL
        CONSTRAINT chk_certificates_type
        CHECK (type IN ('user', 'host', 'service', 'pam')),
    user_id TEXT REFERENCES users(id),
    -- The request whose approval authorized this certificate, closing the
    -- audit chain certificate_request -> decision -> certificate. Nullable
    -- rather than NOT NULL on purpose: SignedReplyHandler treats a failure
    -- to resolve the owner as non-fatal, because the certificate is already
    -- signed and in the requester's hands by the time this row is written,
    -- and a NOT NULL column here would add a second way for that write to
    -- fail and lose the audit record entirely. In practice the signer's
    -- reply always carries the request ID, so this is always populated;
    -- when user_id resolution fails, this is what makes the row
    -- reattachable instead of permanently orphaned.
    certificate_request_id TEXT REFERENCES certificate_requests(id),
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

-- CertificateService.ListForIdentity filters on user_id and sorts on
-- issued_at DESC; the composite covers both, so the per-user history view
-- doesn't sort on every load.
CREATE INDEX idx_certificates_user_id_issued_at ON certificates(user_id, issued_at DESC);
CREATE INDEX idx_certificates_certificate_request_id ON certificates(certificate_request_id);

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
    outcome TEXT NOT NULL
        CONSTRAINT chk_certificate_request_decisions_outcome
        CHECK (outcome IN ('approved', 'denied')),
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

-- "Every decision in this window" — the other first-order audit question,
-- and the one a time-bounded export runs.
CREATE INDEX idx_certificate_request_decisions_decided_at ON certificate_request_decisions(decided_at);

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
CREATE INDEX idx_enrollments_user_id ON enrollments(user_id);

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
