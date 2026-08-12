package model

import "time"

// CertificateRequestStatus tracks a pending web UI approval that the client
// is waiting on over SSE.
type CertificateRequestStatus string

const (
	CertificateRequestStatusPending CertificateRequestStatus = "pending"
	// CertificateRequestStatusSigning means a human approved the request
	// and a signing job was published (see
	// docs/watermill-phase3-sign-queue.md) — not yet terminal. The signer
	// (docs/watermill-phase4-signer-listener.md) and its listener/resolver
	// still need to run before this becomes CertificateRequestStatusApproved.
	// Only used for CertificateTypeUser/CertificateTypeHost — service
	// requests go straight from Pending to CertificateRequestStatusEnrolled,
	// no signer involved at approval time.
	CertificateRequestStatusSigning  CertificateRequestStatus = "signing"
	CertificateRequestStatusApproved CertificateRequestStatus = "approved"
	// CertificateRequestStatusEnrolled is CertificateTypeService's terminal
	// approval state: EnrollmentToken is set, and the certificate itself
	// isn't issued until a later `service retrieve` redeems it — see
	// docs/ssoossh-context.md, "Service enrollment".
	CertificateRequestStatusEnrolled CertificateRequestStatus = "enrolled"
	CertificateRequestStatusDenied   CertificateRequestStatus = "denied"
	CertificateRequestStatusExpired  CertificateRequestStatus = "expired"
	// CertificateRequestStatusFailed is terminal: the signer couldn't
	// produce a certificate (see docs/watermill-phase4-signer-listener.md),
	// or a boot-time sweep invalidated a request left stuck in Signing (see
	// docs/watermill-phase5-invalidation-sweep.md). Distinct from Denied,
	// which means a human said no. No migration needed — status is a
	// free-text TEXT column.
	CertificateRequestStatusFailed CertificateRequestStatus = "failed"
)

// CertificateRequest represents a client's in-flight ask for a user, host,
// or service certificate: created when the client asks (`ssh login`,
// `host sign`, `service enroll`), resolved when a user approves/denies in
// the web UI or it times out. Approving a CertificateTypeService request
// creates an Enrollment rather than issuing a certificate immediately — see
// docs/ssoossh-context.md, "Service enrollment".
//
// The SSE endpoint the client is waiting on watches a request by ID.
// TODO: the pub/sub or channel-based broker backing that watch is not yet
// designed — see server/controller/certrequests.go's SSE handler stub.
type CertificateRequest struct {
	ID   string          `gorm:"column:id;primaryKey"`
	Type CertificateType `gorm:"column:type"`

	// UserID is set once the requester authenticates via OIDC. Absent for
	// an unauthenticated initial "host sign" ask, TODO: confirm host sign
	// requires OIDC login before or after the request row is created.
	UserID *string `gorm:"column:user_id"`

	PublicKey string `gorm:"column:public_key"`

	// Hostname is set only for CertificateTypeHost requests.
	// TODO: i think this would just go in principals
	Hostname string `gorm:"column:hostname"`

	// RequestedOptions is JSON-encoded. Server config (config.CertificateOptions)
	// is the outer bound — the web UI narrows or adjusts, never widens (see
	// root CLAUDE.md Hard Constraints).
	RequestedOptions string `gorm:"column:requested_options"`

	// SourceIP is one of the lifetime-policy signals (see
	// docs/ssoossh-context.md "Certificate lifetime policy" — client-supplied
	// source addresses are unverified input and need a policy ceiling).
	SourceIP string `gorm:"column:source_ip"`

	Status     CertificateRequestStatus `gorm:"column:status"`
	CreatedAt  time.Time                `gorm:"column:created_at"`
	ResolvedAt *time.Time               `gorm:"column:resolved_at"`

	// EnrollmentToken is set when Status is CertificateRequestStatusEnrolled
	// (CertificateTypeService only) — the code `service retrieve` presents
	// to redeem a certificate later. Empty for user/host requests.
	EnrollmentToken string `gorm:"column:enrollment_token"`

	// FailureReason explains a CertificateRequestStatusFailed row: either
	// the signer's error, or that the invalidation sweep found it stranded
	// (see docs/watermill-phase5-invalidation-sweep.md). For operators
	// reading the database — it isn't returned over the API.
	FailureReason string `gorm:"column:failure_reason"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (CertificateRequest) TableName() string { return "certificate_requests" }
