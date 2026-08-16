package model

// The string enumerations the API exposes, kept in their own file so tygo
// can generate the frontend's TypeScript unions from them without also
// emitting the GORM models that live alongside them (see tygo.yaml). Any
// constant added here reaches frontend/src/lib/api/generated/enums.ts on the
// next `make types`.

// CertificateType identifies which of the three certificate types
// (docs/ssoossh-context.md — "Certificate types") a row represents.
type CertificateType string

const (
	CertificateTypeUser    CertificateType = "user"
	CertificateTypeHost    CertificateType = "host"
	CertificateTypeService CertificateType = "service"
	CertificateTypePAM     CertificateType = "pam"
)

// CertificateRequestStatus tracks a pending web UI approval that the client
// is waiting on over SSE.
type CertificateRequestStatus string

const (
	CertificateRequestStatusPending CertificateRequestStatus = "pending"
	// CertificateRequestStatusSigning means a human approved the request
	// and a signing job was published (see
	// docs/signing-pipeline.md) — not yet terminal. The signer
	// (docs/signing-pipeline.md) and its listener/resolver
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
	// produce a certificate (see docs/signing-pipeline.md),
	// or a boot-time sweep invalidated a request left stuck in Signing (see
	// docs/signing-pipeline.md). Distinct from Denied,
	// which means a human said no. No migration needed — status is a
	// free-text TEXT column.
	CertificateRequestStatusFailed CertificateRequestStatus = "failed"
)
