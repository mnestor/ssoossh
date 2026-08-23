package model

// The string enumerations the API exposes, kept in their own file so tygo
// can generate the frontend's TypeScript unions from them without also
// emitting the GORM models that live alongside them (see tygo.yaml). Any
// constant added here reaches frontend/src/lib/api/generated/enums.ts on the
// next `make types`.

// CertificateType identifies which of the three certificate types
// (docs/dev/ssoossh-context.md — "Certificate types") a row represents.
type CertificateType string

const (
	CertificateTypeUser CertificateType = "user"
	// CertificateTypeHost is retired: host certificates were removed (see
	// docs/decisions.md — no secure host verification exists yet). The
	// constant remains only because server/signer and server/certmsg keep
	// dormant host handling (lifetime cap, message fields) that lands with
	// the HSM signer work; nothing creates, stores, or accepts this type.
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
	// Only used for CertificateTypeUser/CertificateTypePAM — service
	// requests go straight from Pending to CertificateRequestStatusEnrolled,
	// no signer involved at approval time.
	CertificateRequestStatusSigning  CertificateRequestStatus = "signing"
	CertificateRequestStatusApproved CertificateRequestStatus = "approved"
	// CertificateRequestStatusEnrolled is CertificateTypeService's terminal
	// approval state: EnrollmentToken is set, and the certificate itself
	// isn't issued until a later `service retrieve` redeems it — see
	// docs/dev/ssoossh-context.md, "Service enrollment".
	CertificateRequestStatusEnrolled CertificateRequestStatus = "enrolled"
	CertificateRequestStatusDenied   CertificateRequestStatus = "denied"
	CertificateRequestStatusExpired  CertificateRequestStatus = "expired"
	// CertificateRequestStatusFailed is terminal: the signer couldn't
	// produce a certificate (see docs/signing-pipeline.md),
	// or a boot-time sweep invalidated a request left stuck in Signing (see
	// docs/signing-pipeline.md). Distinct from Denied,
	// which means a human said no.
	CertificateRequestStatusFailed CertificateRequestStatus = "failed"
)

// Adding a value to CertificateType, CertificateRequestStatus, or
// CertificateRequestDecisionOutcome is no longer a Go-only change: each is
// backed by a CHECK constraint in both migration files and by a matching
// `check:` tag on the model struct, so a new value needs all three updated
// together. That is the point — the constraint is what stops a typo'd
// status from producing a row no guarded UPDATE can ever match again.

// CertificateRequestDecisionOutcome is the decision recorded on a
// CertificateRequestDecision row — see
// docs/dev/changes-next.md.
type CertificateRequestDecisionOutcome string

const (
	CertificateRequestDecisionApproved CertificateRequestDecisionOutcome = "approved"
	CertificateRequestDecisionDenied   CertificateRequestDecisionOutcome = "denied"
)
