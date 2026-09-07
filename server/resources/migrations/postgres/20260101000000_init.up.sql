BEGIN;

-- The whole schema, in one migration. It was sixteen files before, but
-- nothing has shipped, so there is no deployed database whose history they
-- describe — and the incremental ones had begun to cost more than they
-- carried: two tables were being rebuilt on the SQLite side just to widen a
-- CHECK, three tables were created a few files after the columns that
-- reference them, and twenty-odd columns arrived by ALTER TABLE at a
-- distance from the table they belong to, each one carrying its
-- documentation somewhere the reader of the table never looks. Collapsing
-- them means the file you read is the schema you get.
--
-- The collapse is safe only because no database has this schema deployed:
-- golang-migrate never re-runs a version it has already recorded, so a
-- deployed instance would keep whatever its own chain built and quietly
-- diverge from this file. That premise is the entire licence for editing
-- this file, and it expires the first time this schema ships. From then on
-- the rule in .claude/rules/database.md holds without exception: a schema
-- change is a new timestamp-numbered pair in both dialect trees, never an
-- edit here.
--
-- See server/model for the corresponding GORM structs; a column added here
-- must also be added to sqlite/20260101000000_init.up.sql and model/.
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
    -- The person's name as a person reads it — "Ada Lovelace" rather than
    -- "alovelace". Display only: it is shown beside the username in the web
    -- UI and offered to email templates, and it is deliberately not a
    -- certificate principal, not a key ID input, and never an authorization
    -- input. Captured from authentication.fields.name, and overridden by
    -- ldap.fields.name where the directory has the better copy.
    --
    -- Empty string rather than NULL, because every reader wants a string and
    -- "no name captured" and "name is the empty string" are the same answer.
    display_name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    -- Admin-initiated user disable: tracks when a user was disabled and by
    -- which admin. DisabledAt is NULL when the user is not disabled; when
    -- set, the user cannot authenticate and their enrollments expire after
    -- the configured grace period (admin.disable_grace_period).
    disabled_at TIMESTAMPTZ,
    disabled_by_user_id TEXT REFERENCES users(id),
    -- Why a user was disabled, so the next admin deciding whether to
    -- re-enable can see it without reading the audit trail. The audit trail
    -- is the history; this column is the current state, and it survives
    -- audit pruning.
    disabled_reason TEXT NOT NULL DEFAULT '',
    -- What disabled a user, which is what makes auto-re-enable safe: the
    -- sync clears only disables whose source is exactly ldap_sync, so an
    -- admin or SOC disable is never undone automatically. Nullable, so a
    -- row that predates the distinction can never match that exact rule.
    disabled_source TEXT NULL
);
CREATE UNIQUE INDEX idx_users_subject ON users(subject);

-- Host identity is deliberately absent from every type CHECK below
-- (https://mnestor.github.io/ssoossh/project/decisions/): no secure way exists
-- today to verify a host's claim
-- to a hostname, so nothing may issue or store one until something like an
-- ACME challenge provides that.

CREATE TABLE certificate_requests (
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
    created_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
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
    -- Pre-allocation ensures the serial is available to persist at
    -- resolution without waiting for the signer, avoiding burned serials
    -- on signing failures.
    serial_number BIGINT,

    -- The approval page is bound to the first browser that opens it. On the
    -- first document GET of /approve/<id> the server mints a claim token,
    -- sets it as a cookie scoped to that path, and stores its hash here;
    -- every later GET must present the matching cookie or is turned away.
    -- See service.CertRequestService.ClaimApprovalPage and
    -- middleware.ApprovalClaimMiddleware.
    --
    -- Hex SHA-256 of the claim cookie's value, never the value itself, so a
    -- database read does not yield a cookie that unlocks someone's pending
    -- approval page. NULL means unclaimed.
    claim_token_hash TEXT,
    -- When the claim happened. Feeds the cookie-blocked heuristic: a claimed
    -- request revisited cookieless by the same user agent shortly after
    -- claiming is a browser refusing cookies, not a second client.
    claimed_at TIMESTAMPTZ,
    -- User agent that claimed the page, kept for the same heuristic and for
    -- mismatch logging (a second client hitting a claimed page is a
    -- high-signal phishing indicator).
    claim_user_agent TEXT NOT NULL DEFAULT '',

    -- The short code a console displays for a human to type into the web
    -- UI, normalized (Crockford Base32, no separators). Console requests
    -- only; empty for every other type. It is a lookup key for an
    -- already-authenticated approver, not a capability — resolving one
    -- needs a session. Unique among still-approvable rows, enforced by the
    -- partial index below.
    -- See https://mnestor.github.io/ssoossh/concepts/console-flow/.
    user_code TEXT NOT NULL DEFAULT '',

    -- Host context: what a PAM or console module can say about the process
    -- and machine asking. See
    -- https://mnestor.github.io/ssoossh/internals/host-context/.
    --
    -- Every column in this block is self-reported by an unauthenticated
    -- caller, bounded on the way in, and rendered as a claim. They exist so
    -- an approver of a sudo can see which command is asking on which
    -- machine, and so the audit line joins against the host's own logs.
    --
    -- Which machine, which PAM service, which terminal, and whether
    -- PAM_RHOST says this is not a console at all.
    hostname TEXT NOT NULL DEFAULT '',
    pam_service TEXT NOT NULL DEFAULT '',
    tty TEXT NOT NULL DEFAULT '',
    remote_host TEXT NOT NULL DEFAULT '',
    -- PAM_RUSER: who invoked the service, as opposed to username, the
    -- account being authenticated. Under su or sudo's targetpw the two
    -- differ.
    requesting_user TEXT NOT NULL DEFAULT '',
    -- The PAM host process's command line, e.g. "sudo -i".
    process TEXT NOT NULL DEFAULT '',
    -- Process identity on the host. NULL means not reported; 0 is a value,
    -- and on Windows there is no gid at all, which is why none of these
    -- takes a default.
    --
    -- caller_gid is INTEGER where the other three are BIGINT. That is not a
    -- distinction worth defending, only one worth not changing silently:
    -- the goldens in test/migration pin these types, so widening it is a
    -- schema change and belongs in a change that says so.
    caller_uid BIGINT,
    caller_gid INTEGER,
    caller_pid BIGINT,
    caller_ppid BIGINT,
    -- Stable per-install identifier, so a host survives a rename in the
    -- trail.
    machine_id TEXT NOT NULL DEFAULT '',
    -- os-release PRETTY_NAME plus uname -s -r.
    os TEXT NOT NULL DEFAULT '',
    -- Module name and version, and its configured mode argument.
    client TEXT NOT NULL DEFAULT '',
    client_mode TEXT NOT NULL DEFAULT '',
    -- The host's own clock when it built the request; skew is visible here.
    client_time TIMESTAMPTZ,
    -- JSON []string of SHA256 fingerprints of the keys in the module's
    -- trusted-ca-file, so the server can warn before the host rejects.
    trusted_ca_fingerprints TEXT NOT NULL DEFAULT ''
);

-- The sweep is the only query that filters on status alone, and it pairs it
-- with a created_at range (service.SweepStrandedRequests). Every other
-- status predicate is `id = ? AND status = ?`, a primary-key lookup that
-- doesn't use this index at all, so the range column earns its place here.
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

CREATE TABLE certificates (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL
        CONSTRAINT chk_certificates_type
        CHECK (type IN ('user', 'service', 'pam', 'console')),
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
    -- unrelated certificates.
    serial_number BIGINT NOT NULL UNIQUE,
    key_id TEXT NOT NULL DEFAULT '',
    principals TEXT NOT NULL DEFAULT '',
    critical_options TEXT NOT NULL DEFAULT '',
    extensions TEXT NOT NULL DEFAULT '',
    issued_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

-- CertificateService.ListForIdentity filters on user_id and sorts on
-- issued_at DESC; the composite covers both, so the per-user history view
-- doesn't sort on every load.
CREATE INDEX idx_certificates_user_id_issued_at ON certificates(user_id, issued_at DESC);
CREATE INDEX idx_certificates_certificate_request_id ON certificates(certificate_request_id);

-- The audit record of a single Approve/Deny decision. One row per decision,
-- ever, inserted once and never updated or deleted — see
-- server/model/certificate_request_decision.go. Kept in its own table
-- rather than as columns on certificate_requests: that table is the busy,
-- read/write
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
    id TEXT PRIMARY KEY NOT NULL,
    -- certificate_request_id is a plain copied ID, not a foreign key. The
    -- decisions table is permanent and append-only. Pruning certificate_requests
    -- is blocked by the FK or silently deletes the audit record via CASCADE, both
    -- unacceptable. Keeping copied values (like decider identity) avoids this:
    -- the audit record outlives the request it describes, and retention policy
    -- can be applied per-table independently.
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
    decided_at TIMESTAMPTZ NOT NULL,
    -- Why an approval got the lifetime and extensions it got: the winning
    -- policy tier, the condition it matched, the source rule, the ceilings,
    -- and the effective values, as one structured JSON document (see
    -- service.PolicyExplanation). Empty for denials. See
    -- https://mnestor.github.io/ssoossh/operations/certificate-policy/.
    policy_explanation TEXT NOT NULL DEFAULT '',
    -- What the approval granted: the selected principals and the narrowed
    -- options, JSON-encoded. The signing job carries both; persisting them
    -- here means a failed signing does not leave the decision's content in
    -- the log alone. Empty for denials.
    principals TEXT NOT NULL DEFAULT '',
    granted_options TEXT NOT NULL DEFAULT '',

    -- The host context, snapshotted onto the decision. See
    -- https://mnestor.github.io/ssoossh/internals/host-context/.
    --
    -- certificate_requests already holds all of this, and the approval page
    -- reads it from there. The certificate history cannot: a certificate
    -- links to its request, but this table is the permanent, append-only
    -- record and deliberately copies rather than references, so that
    -- requests can be pruned without taking the audit trail with them (see
    -- model.CertificateRequestDecision). Reading the host context through
    -- the request would make the certificate view the one place that breaks
    -- the day pruning lands.
    --
    -- So it is copied here, at decision time, the same way the approver's
    -- identity, address and headers already are. The compact set, matching
    -- what every cert.* audit event carries (service.hostContextDetail) —
    -- the long tail (caller pids, os, client clock, CA fingerprints) stays
    -- on the request, where cert.requested records it.
    --
    -- reported_username and reported_hostname, not username and hostname:
    -- the table already has a username, and it is the approver's. These two
    -- are the requester's — "who and where asked" — and which pair of
    -- request columns they come from depends on the type, which is what
    -- model.CertificateRequest.ReportedIdentity decides.
    reported_username TEXT NOT NULL DEFAULT '',
    reported_hostname TEXT NOT NULL DEFAULT '',
    pam_service TEXT NOT NULL DEFAULT '',
    tty TEXT NOT NULL DEFAULT '',
    remote_host TEXT NOT NULL DEFAULT '',
    requesting_user TEXT NOT NULL DEFAULT '',
    process TEXT NOT NULL DEFAULT '',
    machine_id TEXT NOT NULL DEFAULT '',
    client TEXT NOT NULL DEFAULT ''
);

-- "Everything this person decided" — a common audit question, and without
-- this index it's a full table scan.
CREATE INDEX idx_certificate_request_decisions_subject ON certificate_request_decisions(subject);

-- "Every approval not from the office range" — the source-network signal
-- the certificate lifetime policy acts on. See
-- https://mnestor.github.io/ssoossh/operations/certificate-policy/.
CREATE INDEX idx_certificate_request_decisions_source_ip ON certificate_request_decisions(source_ip);

-- "Every decision in this window" — the other first-order audit question,
-- and the one a time-bounded export runs.
CREATE INDEX idx_certificate_request_decisions_decided_at ON certificate_request_decisions(decided_at);

-- Everything signing needs is fixed here at approval time, never re-derived
-- when `service retrieve` redeems the code (the evaluate-at-enrollment-time
-- contract; see
-- https://mnestor.github.io/ssoossh/operations/certificate-policy/).
CREATE TABLE enrollments (
    id TEXT PRIMARY KEY NOT NULL,
    code TEXT NOT NULL,
    public_key TEXT NOT NULL,
    option_set TEXT NOT NULL DEFAULT '',
    -- Key ID and principals, likewise fixed at approval. principals is a
    -- JSON-encoded []string.
    key_id TEXT NOT NULL DEFAULT '',
    principals TEXT NOT NULL DEFAULT '',
    -- The enrollment's service account, denormalized out of the principals
    -- JSON array it has always been the sole element of (see
    -- CertRequestService.approveServiceEnrollment). Ownership is a query —
    -- "every enrollment for the accounts I hold" — and a query cannot reach
    -- into a JSON string portably across both dialects. See
    -- https://mnestor.github.io/ssoossh/concepts/service-certificates/.
    --
    -- NOT NULL DEFAULT '' rather than nullable: '' is the honest value for a
    -- row whose principals never parsed, and it matches no service account,
    -- so such a row is owned by nobody while staying visible to auditors.
    service_account TEXT NOT NULL DEFAULT '',
    -- Links back to the approved request, keeping certificates issued at
    -- retrieval time on the same audit chain as the approval decision.
    certificate_request_id TEXT REFERENCES certificate_requests(id),
    user_id TEXT NOT NULL REFERENCES users(id),
    -- The address a notification about this enrollment goes to instead of
    -- fanning out to every holder of its service account. NOT NULL
    -- DEFAULT '' matching service_account: an email address is never
    -- legitimately empty, so '' is an unambiguous "unset" that needs no
    -- three-valued logic in the delivery branch.
    notification_email TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    -- Bounds the code, not the certificates it produces: past this,
    -- `service retrieve` stops redeeming. From
    -- cert_options.service.enrollment_duration.
    expires_at TIMESTAMPTZ NOT NULL,
    -- How long each certificate redeemed from this enrollment is valid for,
    -- measured from that redemption rather than from approval. Nullable so
    -- a missing value stays distinct from a stored zero, which means an
    -- approval genuinely computed a zero-length certificate and must fail
    -- at the signer — see EnrollmentService.Retrieve.
    certificate_duration_seconds BIGINT,
    -- First successful redemption. Audit detail, not a single-use gate:
    -- codes stay redeemable until expires_at.
    redeemed_at TIMESTAMPTZ,
    -- The expiry reminder's send-once claim. Every instance runs the sweep
    -- and the queue group deduplicates consumption rather than publication,
    -- so the claim has to live here: the sweep takes it with a guarded
    -- UPDATE and publishes only when that reports one row. Nullable on
    -- purpose — IS NULL is the claim.
    expiry_reminder_sent_at TIMESTAMPTZ,
    -- The expired-attempt notification's rate-limit claim. A broken cron job
    -- retries an expired code forever, so this one is a window rather than a
    -- one-shot: the guarded UPDATE also matches a row whose timestamp is
    -- older than the window, letting the next attempt after it claim again.
    last_expired_attempt_notified_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_enrollments_code ON enrollments(code);
CREATE INDEX idx_enrollments_user_id ON enrollments(user_id);

-- The ownership query: every enrollment for a set of service accounts.
CREATE INDEX idx_enrollments_service_account ON enrollments(service_account);

-- The reminder sweep's query: unexpired enrollments inside the lead window
-- with no reminder claimed. expires_at alone is not enough of a filter —
-- every enrollment ever created has one, and all but a few have already
-- been reminded.
CREATE INDEX idx_enrollments_expiry_reminder ON enrollments(expiry_reminder_sent_at, expires_at);

-- One row per `service retrieve` redemption, for the approving user and
-- auditors to read back. Codes are reusable until the enrollment expires,
-- so an enrollment can have many.
CREATE TABLE enrollment_retrievals (
    id TEXT PRIMARY KEY NOT NULL,
    enrollment_id TEXT NOT NULL REFERENCES enrollments(id),
    source_ip TEXT NOT NULL DEFAULT '',
    certificate_serial BIGINT NOT NULL,
    retrieved_at TIMESTAMPTZ NOT NULL,
    succeeded BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX idx_enrollment_retrievals_enrollment_id ON enrollment_retrievals(enrollment_id);

-- A service certificate is tied to the redemption that produced it by
-- serial, not by a foreign key: EnrollmentService.Retrieve pre-allocates the
-- serial onto the enrollment_retrievals row before queueing the signing job,
-- and the same value lands on the certificates row the signed reply writes.
--
-- The certificate-history query walks that join for every certificate it
-- returns, so the retrieval side needs an index of its own. The index above
-- covers enrollment_id, which this lookup does not have — finding the
-- retrieval is the whole point of it.
CREATE INDEX idx_enrollment_retrievals_certificate_serial
    ON enrollment_retrievals(certificate_serial);

-- One row per enrollment reassignment. An enrollment can be reassigned
-- multiple times, so this is an append-only audit log. Unlike enrollments
-- (busy pipeline table), this carries only an audit record: scalar columns
-- for indexability, no foreign key constraints that would block enrollment
-- cleanup under a future retention policy.
--
-- The distinction between from_user_id and reassigned_by_user_id matters:
-- an owner reassigning their own enrollment has them both pointing to the
-- same user, while an admin reassigning someone else's has them as different
-- people. Both are recorded so auditors can distinguish self-service
-- reassignment from admin-initiated transfer.
CREATE TABLE enrollment_reassignments (
    id TEXT PRIMARY KEY NOT NULL,
    enrollment_id TEXT NOT NULL,
    from_user_id TEXT NOT NULL,
    to_user_id TEXT NOT NULL,
    reassigned_by_user_id TEXT NOT NULL,
    reassigned_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_enrollment_reassignments_enrollment_id ON enrollment_reassignments(enrollment_id);
CREATE INDEX idx_enrollment_reassignments_reassigned_by ON enrollment_reassignments(reassigned_by_user_id);
CREATE INDEX idx_enrollment_reassignments_reassigned_at ON enrollment_reassignments(reassigned_at);

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
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX idx_notification_preferences_user_kind
    ON notification_preferences(user_id, kind);

-- The append-only administrative audit stream. See
-- https://mnestor.github.io/ssoossh/operations/audit-log/ and
-- model.AuditEvent.
--
-- No foreign keys, by design: an audit entry must read the same in five
-- years as it did the day it was written, so identity is copied into the
-- payload as a snapshot rather than referenced. The two user-id columns are
-- indexed grouping keys for the UI's two timelines ("everything this
-- account did" and "everything done to this account"), never references and
-- never authoritative.
--
-- This table is a bounded cache pruned on a schedule; the shipped
-- type=audit log is the archive.
CREATE TABLE audit_events (
    id             TEXT PRIMARY KEY NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    actor_user_id  TEXT NULL,
    target_user_id TEXT NULL,
    payload        TEXT NOT NULL DEFAULT ''
);

-- Both timelines are "this user, newest first", so the sort column is part
-- of each index rather than a separate sort step.
CREATE INDEX idx_audit_events_actor ON audit_events (actor_user_id, created_at);
CREATE INDEX idx_audit_events_target ON audit_events (target_user_id, created_at);

-- The recent-activity feed and the retention sweep both order by age alone.
CREATE INDEX idx_audit_events_created_at ON audit_events (created_at);

-- LDAP sync bookkeeping, one row per user who has logged in while LDAP was
-- enabled. Only known users sync: the server never enumerates the
-- directory, which keeps the user set self-selecting. See
-- https://mnestor.github.io/ssoossh/operations/ldap/.
CREATE TABLE user_ldap (
    user_id            TEXT PRIMARY KEY NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dn                 TEXT NOT NULL DEFAULT '',
    -- The entry's unique, immutable identifier (entryUUID, objectGUID,
    -- ipaUniqueID — named by ldap.id_attribute), and what makes a directory
    -- rename survivable. Resolution without it is DN first, then the
    -- rendered user_filter, and both move: a DN changes when someone is
    -- moved between OUs, and a user_filter keyed on {{.Username}} stops
    -- matching the moment they are renamed, so a rename looked exactly like
    -- a deletion and walked the account toward auto-disable. With an ID
    -- stored, the sync searches by it first and re-anchors the DN instead.
    --
    -- Empty string on deployments that leave ldap.id_attribute unset; those
    -- keep the DN-then-filter path unchanged.
    directory_id       TEXT NOT NULL DEFAULT '',
    attributes         TEXT NOT NULL DEFAULT '',
    last_seen_at       TIMESTAMPTZ NULL,
    last_synced_at     TIMESTAMPTZ NULL,
    consecutive_misses INTEGER NOT NULL DEFAULT 0,
    -- When the entry was first found to be missing, which is what makes
    -- ldap.sync.disable_after a duration rather than a pass count. A count
    -- was not a measure of time: scheduled jobs are not leader-elected, so
    -- three replicas produced three increments per interval, and an
    -- operator-triggered sync added more. Set on the first miss, cleared on
    -- any find, and compared against elapsed time; NULL means the entry is
    -- not currently missing.
    first_missing_at   TIMESTAMPTZ NULL,
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL
);

-- Partial, because the empty string is the common value and indexing it
-- would be indexing "no ID". The lookup this serves is by a specific ID.
CREATE INDEX idx_user_ldap_directory_id ON user_ldap (directory_id) WHERE directory_id <> '';

-- Persisted group membership, for notification fan-out and display. Never
-- an authorization input: authorization reads the session identity only
-- (see https://mnestor.github.io/ssoossh/internals/invariants/).
--
-- Rows rather than JSON so "everyone in soc" is one indexed query. Unique
-- per (user, group, source) so the two capture paths never collide and
-- either can be replaced without touching the other.
CREATE TABLE user_groups (
    id            TEXT PRIMARY KEY NOT NULL,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_name    TEXT NOT NULL,
    source        TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at  TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_user_groups_source CHECK (source IN ('oidc','ldap'))
);

CREATE UNIQUE INDEX idx_user_groups_unique ON user_groups (user_id, group_name, source);
CREATE INDEX idx_user_groups_user ON user_groups (user_id);
-- The fan-out query: everyone in a named group.
CREATE INDEX idx_user_groups_name ON user_groups (group_name);

-- One row per directory sync pass, so a sync that ran can be told from one
-- that never fired.
--
-- Sync otherwise reports only to the log, which means a UI has nowhere to
-- read the outcome from and an operator asking "is the sync even running"
-- has to go and read a log file on whichever instance happened to run it.
-- That is also what an operator-triggered sync needs in order to report
-- anything at all.
CREATE TABLE ldap_sync_runs (
    id             TEXT PRIMARY KEY NOT NULL,

    started_at     TIMESTAMPTZ NOT NULL,
    -- NULL while the pass is still running, which is also how a run that
    -- died with its process reads afterwards.
    finished_at    TIMESTAMPTZ NULL,

    -- 'schedule' or 'manual'. Named trigger_source rather than trigger
    -- because TRIGGER is a reserved word in PostgreSQL and this schema is
    -- kept identical across both dialects.
    trigger_source TEXT NOT NULL,

    -- A dry run reads the directory and reports what it would have done,
    -- writing nothing but this row. It is what makes the button safe to
    -- press during an incident.
    dry_run        BOOLEAN NOT NULL DEFAULT FALSE,

    -- The admin who pressed the button. NULL for a scheduled pass, and
    -- nulled rather than deleted with the user, since the run happened.
    actor_user_id  TEXT NULL REFERENCES users(id) ON DELETE SET NULL,

    -- The host that ran it. Jobs are not leader-elected, so every instance
    -- runs its own pass and "which log do I go and read" is a real
    -- question.
    instance       TEXT NOT NULL DEFAULT '',

    users_seen     INTEGER NOT NULL DEFAULT 0,
    found          INTEGER NOT NULL DEFAULT 0,
    missing        INTEGER NOT NULL DEFAULT 0,
    failed         INTEGER NOT NULL DEFAULT 0,
    disabled       INTEGER NOT NULL DEFAULT 0,
    reenabled      INTEGER NOT NULL DEFAULT 0,

    -- The reason the pass could not run: an unreachable directory, a failed
    -- bind. Empty for a pass that completed, whatever it concluded about
    -- individual users.
    error_message  TEXT NOT NULL DEFAULT ''
);

-- The status panel's query: the most recent run.
CREATE INDEX idx_ldap_sync_runs_started ON ldap_sync_runs (started_at DESC);

CREATE TABLE ca_signer_keys (
    fingerprint TEXT PRIMARY KEY NOT NULL,
    public_key TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_ca_signer_keys_expires_at ON ca_signer_keys(expires_at);

CREATE TABLE server_secrets (
    name TEXT PRIMARY KEY NOT NULL,
    value BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

COMMIT;
