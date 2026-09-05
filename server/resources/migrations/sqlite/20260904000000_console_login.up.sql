-- Console login: a fourth certificate type, plus the console context an
-- approver needs to recognise the login they are being asked to authorize.
-- See docs/proposals/console-login-pam.md.
--
-- SQLite cannot alter a CHECK constraint, so both tables carrying a
-- certificate-type CHECK are rebuilt with the documented 12-step procedure
-- (https://sqlite.org/lang_altertable.html#otheralter). This is the first
-- migration here to do that, so the reasoning is spelled out:
--
--   * Foreign keys are disabled for the duration. certificates and
--     enrollment_retrievals both REFERENCES certificate_requests(id), and
--     with enforcement on, DROP TABLE performs an implicit DELETE that
--     those child rows would refuse. golang-migrate runs each file through
--     one db.Exec with NoTxWrap set, so the whole file lands on a single
--     pooled connection and the PRAGMA applies to every statement below it.
--   * The rename comes last and happens while foreign keys are off, so
--     SQLite leaves the REFERENCES clauses in other tables alone — they
--     already name certificate_requests, which is the name being restored.
--   * Order is CREATE, INSERT, DROP, RENAME so the only statement that can
--     realistically fail (the copy) runs before anything is dropped.
PRAGMA foreign_keys=OFF;

-- Host identity is deliberately absent from the type CHECK
-- (https://mnestor.github.io/ssoossh/project/decisions/): no secure way exists
-- today to verify a host's claim to a hostname.
CREATE TABLE certificate_requests_new (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL
        CONSTRAINT chk_certificate_requests_type
        CHECK (type IN ('user', 'service', 'pam', 'console')),
    user_id TEXT REFERENCES users(id),
    public_key TEXT NOT NULL,
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
    -- only; empty for every other type.
    local_username TEXT NOT NULL DEFAULT '',
    local_hostname TEXT NOT NULL DEFAULT '',
    -- ServiceAccount is set only for CertificateTypeService requests: the
    -- service account the certificate is for, selected during approval.
    service_account TEXT NOT NULL DEFAULT '',
    -- SerialNumber is the pre-allocated certificate serial for user/PAM
    -- requests, set at approval time before signing. Null for service
    -- enrollments (they don't produce certificates at approval time).
    serial_number INTEGER,
    -- Hex SHA-256 of the claim cookie's value, never the value itself, so a
    -- database read does not yield a cookie that unlocks someone's pending
    -- approval page. NULL means unclaimed.
    claim_token_hash TEXT,
    claimed_at DATETIME,
    claim_user_agent TEXT NOT NULL DEFAULT '',
    -- The short code a console displays for a human to type into the web
    -- UI, normalized (Crockford Base32, no separators). Console requests
    -- only; empty for every other type. It is a lookup key for an
    -- already-authenticated approver, not a capability — resolving one
    -- needs a session. Unique among still-approvable rows, enforced by the
    -- partial index below.
    user_code TEXT NOT NULL DEFAULT '',
    -- Console context, all of it self-reported by an unauthenticated
    -- caller and rendered as such: which machine, which PAM service, which
    -- terminal, and whether PAM_RHOST says this is not a console at all.
    -- Bounded on the way in so a caller cannot write arbitrary volume here.
    hostname TEXT NOT NULL DEFAULT '',
    pam_service TEXT NOT NULL DEFAULT '',
    tty TEXT NOT NULL DEFAULT '',
    remote_host TEXT NOT NULL DEFAULT ''
);

INSERT INTO certificate_requests_new (
    id, type, user_id, public_key, username, requested_options, source_ip,
    status, created_at, resolved_at, enrollment_token, failure_reason,
    local_username, local_hostname, service_account, serial_number,
    claim_token_hash, claimed_at, claim_user_agent
)
SELECT
    id, type, user_id, public_key, username, requested_options, source_ip,
    status, created_at, resolved_at, enrollment_token, failure_reason,
    local_username, local_hostname, service_account, serial_number,
    claim_token_hash, claimed_at, claim_user_agent
FROM certificate_requests;

DROP TABLE certificate_requests;
ALTER TABLE certificate_requests_new RENAME TO certificate_requests;

-- The sweep is the only query that filters on status alone, and it pairs it
-- with a created_at range (service.SweepStrandedRequests).
CREATE INDEX idx_certificate_requests_status_created_at ON certificate_requests(status, created_at);

-- Declared foreign key, so it gets an index: without one, any pre-delete
-- check or ON DELETE action on users degrades to a full scan.
CREATE INDEX idx_certificate_requests_user_id ON certificate_requests(user_id);

-- Uniqueness over live rows only. Two pending requests sharing a code would
-- let one approver's typed code resolve to a stranger's request; a resolved
-- one is no longer reachable by code, so retiring it from the index keeps
-- the 40-bit space from filling up over the life of a deployment.
CREATE UNIQUE INDEX idx_certificate_requests_user_code
    ON certificate_requests(user_code)
    WHERE user_code <> '' AND status IN ('pending', 'signing');

CREATE TABLE certificates_new (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL
        CONSTRAINT chk_certificates_type
        CHECK (type IN ('user', 'service', 'pam', 'console')),
    user_id TEXT REFERENCES users(id),
    -- The request whose approval authorized this certificate, closing the
    -- audit chain certificate_request -> decision -> certificate. Nullable
    -- rather than NOT NULL on purpose: SignedReplyHandler treats a failure
    -- to resolve the owner as non-fatal, and a NOT NULL column here would
    -- add a second way for that write to fail and lose the audit record.
    certificate_request_id TEXT REFERENCES certificate_requests(id),
    public_key_fingerprint TEXT NOT NULL,
    -- Pre-allocated at approval time. The UNIQUE constraint converts
    -- collisions into failed inserts rather than silently revoking
    -- unrelated certificates.
    serial_number INTEGER NOT NULL UNIQUE,
    key_id TEXT NOT NULL DEFAULT '',
    principals TEXT NOT NULL DEFAULT '',
    critical_options TEXT NOT NULL DEFAULT '',
    extensions TEXT NOT NULL DEFAULT '',
    issued_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL
);

INSERT INTO certificates_new (
    id, type, user_id, certificate_request_id, public_key_fingerprint,
    serial_number, key_id, principals, critical_options, extensions,
    issued_at, expires_at
)
SELECT
    id, type, user_id, certificate_request_id, public_key_fingerprint,
    serial_number, key_id, principals, critical_options, extensions,
    issued_at, expires_at
FROM certificates;

DROP TABLE certificates;
ALTER TABLE certificates_new RENAME TO certificates;

-- CertificateService.ListForIdentity filters on user_id and sorts on
-- issued_at DESC; the composite covers both.
CREATE INDEX idx_certificates_user_id_issued_at ON certificates(user_id, issued_at DESC);
CREATE INDEX idx_certificates_certificate_request_id ON certificates(certificate_request_id);

PRAGMA foreign_keys=ON;
