PRAGMA foreign_keys=OFF;
BEGIN;

-- The whole schema, in one migration. It was six before, but nothing has
-- shipped, so there is no deployed database whose history they describe —
-- and the incremental ones had begun to cost more than they carried: two
-- tables were already being rebuilt with SQLite's copy/drop/rename dance
-- just to widen a CHECK, and three columns were reaching tables created a
-- few files earlier. Collapsing them means the file you read is the schema
-- you get. Reshape this one freely until the first release; after that,
-- add migrations rather than editing it.
--
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
    id TEXT PRIMARY KEY NOT NULL,
    subject TEXT NOT NULL,
    username TEXT NOT NULL,
    email TEXT NOT NULL DEFAULT '',
    other_accounts TEXT NOT NULL DEFAULT '',
    service_accounts TEXT NOT NULL DEFAULT '',
    -- Operator-configured extra OIDC claim fields captured at login, as a
    -- JSON map of template name -> string or array of strings ('{}' when
    -- none are configured). Consumed by key ID templates. See
    -- config.OAuthFields.Extra.
    extra_fields TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX idx_users_subject ON users(subject);

-- Host identity is deliberately absent from every type CHECK below
-- (docs/decisions.md): no secure way exists today to verify a host's claim
-- to a hostname, so nothing may issue or store one until something like an
-- ACME challenge provides that.
CREATE TABLE certificate_requests (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL
        CONSTRAINT chk_certificate_requests_type
        CHECK (type IN ('user', 'service', 'pam')),
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
    -- only; empty for every other type. See
    -- docs/dev/changes-next.md.
    local_username TEXT NOT NULL DEFAULT '',
    local_hostname TEXT NOT NULL DEFAULT '',
    -- ServiceAccount is set only for CertificateTypeService requests: the
    -- service account the certificate is for, selected during approval.
    service_account TEXT NOT NULL DEFAULT '',
    -- SerialNumber is the pre-allocated certificate serial for user/PAM
    -- requests, set at approval time before signing. Null for service
    -- enrollments (they don't produce certificates at approval time).
    -- Pre-allocation ensures the serial is available to persist at
    -- resolution without waiting for the signer, avoiding burned serials
    -- on signing failures (see docs/dev/changes-next.md items 5 and 11).
    serial_number INTEGER
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
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL
        CONSTRAINT chk_certificates_type
        CHECK (type IN ('user', 'service', 'pam')),
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
    public_key_fingerprint TEXT NOT NULL,
    -- SerialNumber is pre-allocated at approval time (before signing is
    -- queued), ensuring it's available to persist at request resolution
    -- without waiting for the signer. The UNIQUE constraint converts
    -- collisions into failed inserts rather than silently revoking
    -- unrelated certificates (see docs/dev/changes-next.md item 11).
    serial_number INTEGER NOT NULL UNIQUE,
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
-- docs/dev/changes-next.md. Kept in its own table rather than
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
--
-- certificate_request_id is a plain copied ID, not a foreign key. The
-- decisions table is permanent and append-only (see docs/dev/changes-next.md
-- section "First: decide the retention story"). Pruning certificate_requests
-- is blocked by the FK or silently deletes the audit record via CASCADE, both
-- unacceptable. Keeping copied values (like decider identity) avoids this:
-- the audit record outlives the request it describes, and retention policy
-- can be applied per-table independently (see docs/dev/changes-next.md).
CREATE TABLE certificate_request_decisions (
    id TEXT PRIMARY KEY NOT NULL,
    certificate_request_id TEXT NOT NULL UNIQUE,
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

-- Everything signing needs is fixed here at approval time, never re-derived
-- when `service retrieve` redeems the code (the evaluate-at-enrollment-time
-- contract; see docs/certificate-lifetime-policy.md).
CREATE TABLE enrollments (
    id TEXT PRIMARY KEY NOT NULL,
    code TEXT NOT NULL,
    public_key TEXT NOT NULL,
    option_set TEXT NOT NULL DEFAULT '',
    -- Key ID and principals, likewise fixed at approval. principals is a
    -- JSON-encoded []string.
    key_id TEXT NOT NULL DEFAULT '',
    principals TEXT NOT NULL DEFAULT '',
    -- Links back to the approved request, keeping certificates issued at
    -- retrieval time on the same audit chain as the approval decision.
    certificate_request_id TEXT REFERENCES certificate_requests(id),
    user_id TEXT NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL,
    -- Bounds the code, not the certificates it produces: past this,
    -- `service retrieve` stops redeeming. From
    -- cert_options.service.enrollment_duration.
    expires_at DATETIME NOT NULL,
    -- How long each certificate redeemed from this enrollment is valid for,
    -- measured from that redemption rather than from approval. Nullable so
    -- a missing value stays distinct from a stored zero, which means an
    -- approval genuinely computed a zero-length certificate and must fail
    -- at the signer — see EnrollmentService.Retrieve.
    certificate_duration_seconds INTEGER,
    -- First successful redemption. Audit detail, not a single-use gate:
    -- codes stay redeemable until expires_at.
    redeemed_at DATETIME
);
CREATE UNIQUE INDEX idx_enrollments_code ON enrollments(code);
CREATE INDEX idx_enrollments_user_id ON enrollments(user_id);

-- One row per `service retrieve` redemption, for the approving user and
-- auditors to read back. Codes are reusable until the enrollment expires,
-- so an enrollment can have many.
CREATE TABLE enrollment_retrievals (
    id TEXT PRIMARY KEY NOT NULL,
    enrollment_id TEXT NOT NULL REFERENCES enrollments(id),
    source_ip TEXT NOT NULL DEFAULT '',
    certificate_serial INTEGER NOT NULL,
    retrieved_at DATETIME NOT NULL,
    succeeded INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_enrollment_retrievals_enrollment_id ON enrollment_retrievals(enrollment_id);

-- One row per (user, notification kind) the user has made an explicit
-- choice about. Absence means the kind's registered default, which is what
-- lets a new notification kind ship without a schema change or a backfill
-- (see server/notify and server/model/notification_preference.go).
--
-- kind is deliberately not constrained to a fixed list: the authority for
-- which kinds exist is the Go registry, and a row for a kind that has since
-- been removed must stay inert rather than block a migration.
CREATE TABLE notification_preferences (
    id TEXT PRIMARY KEY NOT NULL,
    user_id TEXT NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX idx_notification_preferences_user_kind
    ON notification_preferences(user_id, kind);

CREATE TABLE ca_signer_keys (
    fingerprint TEXT PRIMARY KEY NOT NULL,
    public_key TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX idx_ca_signer_keys_expires_at ON ca_signer_keys(expires_at);

CREATE TABLE server_secrets (
    name TEXT PRIMARY KEY NOT NULL,
    value BLOB NOT NULL,
    created_at DATETIME NOT NULL
);

COMMIT;
PRAGMA foreign_keys=ON;
