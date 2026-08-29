package model

import "time"

// CertificateRequest represents a client's in-flight ask for a user, host,
// or service certificate: created when the client asks (`ssh login`,
// `host sign`, `service enroll`), resolved when a user approves/denies in
// the web UI or it times out. Approving a CertificateTypeService request
// creates an Enrollment rather than issuing a certificate immediately — see
// docs/internals/design-brief.md, "Service enrollment".
//
// The SSE endpoint the client is waiting on watches a request by ID.
// TODO: the pub/sub or channel-based broker backing that watch is not yet
// designed — see server/controller/certrequests.go's SSE handler stub.
type CertificateRequest struct {
	ID string `gorm:"column:id;primaryKey"`

	// Type carries a CHECK constraint mirroring the migration's — see
	// model.Certificate.Type for why the tag is duplicated there.
	Type CertificateType `gorm:"column:type;check:chk_certificate_requests_type,type IN ('user','service','pam')"`

	// UserID is set once the requester authenticates via OIDC. Absent for
	// an unauthenticated initial "host sign" ask, TODO: confirm host sign
	// requires OIDC login before or after the request row is created.
	UserID *string `gorm:"column:user_id"`

	PublicKey string `gorm:"column:public_key"`

	// Username is set only for CertificateTypePAM requests: the local
	// account the PAM module is authenticating (e.g. who is running
	// `sudo`). This, not the approver's OIDC identity, is what becomes the
	// issued certificate's principal — see service.resolvePrincipals. The
	// two are usually but not necessarily the same string.
	Username string `gorm:"column:username"`

	// RequestedOptions is JSON-encoded. Server config (config.CertificateOptions)
	// is the outer bound — the web UI narrows or adjusts, never widens (see
	// docs/internals/invariants.md).
	RequestedOptions string `gorm:"column:requested_options"`

	// SourceIP is one of the lifetime-policy signals (see
	// docs/internals/design-brief.md "Certificate lifetime policy" — client-supplied
	// source addresses are unverified input and need a policy ceiling).
	SourceIP string `gorm:"column:source_ip"`

	// LocalUsername and LocalHostname are set only for CertificateTypeUser
	// requests: the OS user and hostname of the client that made the
	// request. For a user cert there is no way to request one except via
	// the local client, so local_user@host is the requester identity, not
	// optional extra context.
	LocalUsername string `gorm:"column:local_username"`
	LocalHostname string `gorm:"column:local_hostname"`

	// ServiceAccount is set only for CertificateTypeService requests: the
	// service account the certificate is for, selected during approval.
	// This closes the schema gap where a service enrollment request had no
	// link to which specific account was being enrolled.
	ServiceAccount string `gorm:"column:service_account"`

	// SerialNumber is the pre-allocated certificate serial for user/PAM
	// requests, set at approval time before signing. Null for service
	// enrollments (they don't produce certificates at approval time) and
	// host requests (not yet supported). Pre-allocation ensures the serial
	// is available to persist at resolution without waiting for the signer.
	// This avoids burning serials on signing failures.
	SerialNumber *uint64 `gorm:"column:serial_number"`

	// Status carries a CHECK constraint mirroring the migration's. Every
	// transition is a guarded UPDATE ... WHERE status = ?, so a value
	// outside the set would strand the row: no guarded update would match
	// it again and the sweep would never see it. The constraint makes that
	// a failed write rather than a silently unreachable request.
	Status     CertificateRequestStatus `gorm:"column:status;check:chk_certificate_requests_status,status IN ('pending','signing','approved','enrolled','denied','expired','failed')"`
	CreatedAt  time.Time                `gorm:"column:created_at"`
	ResolvedAt *time.Time               `gorm:"column:resolved_at"`

	// EnrollmentToken is set when Status is CertificateRequestStatusEnrolled
	// (CertificateTypeService only) — the code `service retrieve` presents
	// to redeem a certificate later. Empty for user/host requests.
	EnrollmentToken string `gorm:"column:enrollment_token"`

	// FailureReason explains a CertificateRequestStatusFailed row: either
	// the signer's error, or that the invalidation sweep found it stranded
	// (see docs/internals/signing-pipeline.md). For operators
	// reading the database — it isn't returned over the API.
	FailureReason string `gorm:"column:failure_reason"`

	// ClaimTokenHash binds the /approve/<id> page to the first browser that
	// fetched it: hex SHA-256 of the claim cookie's value set on that first
	// GET (never the value itself, so a database read cannot mint a working
	// cookie). Nil means the page has not been opened yet. This is the
	// browser-level binding; UserID above is the separate identity-level
	// binding made on the first authenticated touch. See
	// CertRequestService.ClaimApprovalPage.
	ClaimTokenHash *string `gorm:"column:claim_token_hash"`

	// ClaimedAt and ClaimUserAgent exist for ClaimApprovalPage's
	// cookie-blocked heuristic (same user agent, cookieless, shortly after
	// the claim means a browser refusing cookies rather than a second
	// client) and for mismatch logging.
	ClaimedAt      *time.Time `gorm:"column:claimed_at"`
	ClaimUserAgent string     `gorm:"column:claim_user_agent"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (CertificateRequest) TableName() string { return "certificate_requests" }
