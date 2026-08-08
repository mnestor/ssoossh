package model

import "time"

// CertificateRequestStatus tracks a pending web UI approval that the client
// is waiting on over SSE.
type CertificateRequestStatus string

const (
	CertificateRequestStatusPending  CertificateRequestStatus = "pending"
	CertificateRequestStatusApproved CertificateRequestStatus = "approved"
	CertificateRequestStatusDenied   CertificateRequestStatus = "denied"
	CertificateRequestStatusExpired  CertificateRequestStatus = "expired"
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
}

// TableName overrides GORM's default pluralization to match the migration.
func (CertificateRequest) TableName() string { return "certificate_requests" }
