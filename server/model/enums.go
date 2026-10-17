package model

// The string enumerations the API exposes, kept in their own file so tygo
// can generate the frontend's TypeScript unions from them without also
// emitting the GORM models that live alongside them (see tygo.yaml). Any
// constant added here reaches frontend/src/lib/api/generated/enums.ts on the
// next `make types`.

// CertificateType identifies which of the four certificate types
// (https://mnestor.github.io/ssoossh/internals/design-brief/ — "Certificate
// types") a row represents. These four are the whole set: anything else is
// rejected at the service boundary and by the schema's type CHECK. Host
// identity is deliberately not among them
// (https://mnestor.github.io/ssoossh/project/decisions/).
type CertificateType string

const (
	CertificateTypeUser    CertificateType = "user"
	CertificateTypeService CertificateType = "service"
	CertificateTypePAM     CertificateType = "pam"
	// CertificateTypeConsole authenticates an interactive console login
	// through a PAM module on a machine with no browser in front of it:
	// the console displays a short code, the approver types it into the
	// web UI. Distinct from CertificateTypePAM because a console
	// certificate buys a whole session where a PAM one buys a single
	// local operation, and an operator needs to gate, time, and audit
	// those separately
	// (https://mnestor.github.io/ssoossh/concepts/console-flow/).
	CertificateTypeConsole CertificateType = "console"
)

// CertificateRequestStatus tracks a pending web UI approval that the client
// is waiting on over SSE.
type CertificateRequestStatus string

const (
	CertificateRequestStatusPending CertificateRequestStatus = "pending"
	// CertificateRequestStatusSigning means a human approved the request and a
	// signing job was published (see
	// https://mnestor.github.io/ssoossh/internals/architecture/) — not yet
	// terminal. The signer
	// (https://mnestor.github.io/ssoossh/internals/architecture/) and its
	// listener/resolver still need to run before this becomes
	// CertificateRequestStatusApproved. Only used for
	// CertificateTypeUser/CertificateTypePAM — service requests go straight from
	// Pending to CertificateRequestStatusEnrolled, no signer involved at
	// approval time.
	CertificateRequestStatusSigning  CertificateRequestStatus = "signing"
	CertificateRequestStatusApproved CertificateRequestStatus = "approved"
	// CertificateRequestStatusEnrolled is CertificateTypeService's terminal
	// approval state: EnrollmentToken is set, and the certificate itself
	// isn't issued until a later `service retrieve` redeems it — see
	// https://mnestor.github.io/ssoossh/internals/design-brief/, "Service enrollment".
	CertificateRequestStatusEnrolled CertificateRequestStatus = "enrolled"
	CertificateRequestStatusDenied   CertificateRequestStatus = "denied"
	CertificateRequestStatusExpired  CertificateRequestStatus = "expired"
	// CertificateRequestStatusFailed is terminal: the signer couldn't
	// produce a certificate (see https://mnestor.github.io/ssoossh/internals/architecture/),
	// or a boot-time sweep invalidated a request left stuck in Signing (see
	// https://mnestor.github.io/ssoossh/internals/architecture/). Distinct from Denied,
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
// CertificateRequestDecision row.
type CertificateRequestDecisionOutcome string

const (
	CertificateRequestDecisionApproved CertificateRequestDecisionOutcome = "approved"
	CertificateRequestDecisionDenied   CertificateRequestDecisionOutcome = "denied"
)

// DetailLevel is how much a reader is shown: the full page or message, or
// the facts needed to decide or to understand the result.
type DetailLevel string

const (
	// DetailLevelSimplified keeps what a reader needs to decide or to
	// understand the result, and drops the diagnostic detail an expert
	// compares.
	DetailLevelSimplified DetailLevel = "simplified"
	// DetailLevelAdvanced is everything.
	DetailLevelAdvanced DetailLevel = "advanced"
)

// Valid reports whether l is one of the two levels.
func (l DetailLevel) Valid() bool {
	return l == DetailLevelSimplified || l == DetailLevelAdvanced
}

// PosixStatus is the state an NSS account is served in. Never stored:
// it is derived each time the account is read (see PosixUser), so a
// disable anywhere reaches hosts on their next refresh without a second
// write that could fall out of step.
type PosixStatus string

const (
	// PosixStatusActive is an account served normally.
	PosixStatusActive PosixStatus = "active"
	// PosixStatusDisabled is an account the linked person's disable
	// (admin, SOC or the directory sync) or the directory's own disabled
	// flag has locked. It is still served, with nss.disabled_shell as its
	// shell.
	PosixStatusDisabled PosixStatus = "disabled"
	// PosixStatusDeleted is a tombstone: an entry missing from the
	// directory for longer than nss.tombstone_after. Served as disabled
	// until nss.tombstone_retention ends, then not at all.
	PosixStatusDeleted PosixStatus = "deleted"
)
