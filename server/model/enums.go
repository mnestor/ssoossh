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

// NSSEntryState is the one condition the admin directory viewer shows for
// a stored account or group. Several marks can hold at once (a withheld
// row can also have a pending change); the state is the one that most
// decides what a host is served, in the order below. Never stored: derived
// from the row's marks each time it is read.
type NSSEntryState string

const (
	// NSSEntryStateWithheld is a row part of a uid, gid or name collision.
	// Lookups treat it as not found.
	NSSEntryStateWithheld NSSEntryState = "withheld"
	// NSSEntryStateTombstone is a row whose entry has been missing for
	// longer than nss.tombstone_after. An account is served as deleted and
	// a group with no members until nss.tombstone_retention ends, then
	// neither is served.
	NSSEntryStateTombstone NSSEntryState = "tombstone"
	// NSSEntryStateDisabled is an account the directory's disabled flag or
	// the linked person's disable has locked: served with
	// nss.disabled_shell. Never set on a group.
	NSSEntryStateDisabled NSSEntryState = "disabled"
	// NSSEntryStateMissing is a row a full sync did not see. Still served
	// as before until it becomes a tombstone.
	NSSEntryStateMissing NSSEntryState = "missing"
	// NSSEntryStatePending is a row whose directory entry now carries a
	// different uid or gid. Served with the stored numbers until an admin
	// accepts the change or the directory reverts.
	NSSEntryStatePending NSSEntryState = "pending"
	// NSSEntryStateActive is a row served as stored.
	NSSEntryStateActive NSSEntryState = "active"
)

// NSSNotServedReason says why a host's lookup of a stored account or
// group finds nothing, for the admin directory viewer.
type NSSNotServedReason string

const (
	// NSSNotServedWithheld is a row withheld by a collision.
	NSSNotServedWithheld NSSNotServedReason = "withheld"
	// NSSNotServedPastRetention is a tombstone older than
	// nss.tombstone_retention that the purge has not removed yet.
	NSSNotServedPastRetention NSSNotServedReason = "past_retention"
	// NSSNotServedPrivateGroupClash is an account nss.auto_private_groups
	// "true" cannot serve, because a directory group already holds its
	// private group's gid or name.
	NSSNotServedPrivateGroupClash NSSNotServedReason = "private_group_clash"
)

// NSSKeyStatus is the state a fleet API key is in. Never stored: it is
// derived from the key's revocation and expiry each time the key is read,
// so a key that passes its expires_at is expired without a job having to
// run. The web UI shows a fourth state, rotating, for an active key whose
// previous secret is still accepted; the server never sends it.
type NSSKeyStatus string

const (
	// NSSKeyStatusActive is a key hosts may use.
	NSSKeyStatusActive NSSKeyStatus = "active"
	// NSSKeyStatusExpired is a key past its expires_at.
	NSSKeyStatusExpired NSSKeyStatus = "expired"
	// NSSKeyStatusRevoked is a key revoked by someone. Terminal.
	NSSKeyStatusRevoked NSSKeyStatus = "revoked"
)

// NSSKeyPermission is one thing a caller may do with one fleet API key,
// sent with every key so the web UI shows exactly the affordances the
// server will honour.
type NSSKeyPermission string

const (
	// NSSKeyPermissionView reads the key, its usage and its rotation
	// report.
	NSSKeyPermissionView NSSKeyPermission = "view"
	// NSSKeyPermissionRotate issues a new secret, and ends the previous
	// one early.
	NSSKeyPermissionRotate NSSKeyPermission = "rotate"
	// NSSKeyPermissionRevoke revokes the key.
	NSSKeyPermissionRevoke NSSKeyPermission = "revoke"
	// NSSKeyPermissionEdit changes the description, expiry and
	// notification address.
	NSSKeyPermissionEdit NSSKeyPermission = "edit"
	// NSSKeyPermissionAssignGroup changes the group that manages the key.
	NSSKeyPermissionAssignGroup NSSKeyPermission = "assign_group"
)

// NSSRotationBucket is where one source address stands in a fleet key's
// rotation report since the key was last rotated.
type NSSRotationBucket string

const (
	// NSSRotationBucketSwitched is an address whose latest lookup
	// presented the current secret.
	NSSRotationBucketSwitched NSSRotationBucket = "switched"
	// NSSRotationBucketNotSwitched is an address whose latest lookup
	// presented an older secret: it stops resolving accounts when the
	// grace ends.
	NSSRotationBucketNotSwitched NSSRotationBucket = "not_switched"
	// NSSRotationBucketGone is an address seen before the rotation and
	// not since, a host retired or one that has not booted since.
	NSSRotationBucketGone NSSRotationBucket = "gone"
)

// NSSCollisionKind says which unique value an NSSCollision contests, and so
// which table its HolderID and OtherID name.
type NSSCollisionKind string

// The four contested values. POSIX resolves one record per number and per
// name, and account and group names are separate namespaces.
const (
	// NSSCollisionKindUID is two accounts with one uid (posix_users).
	NSSCollisionKindUID NSSCollisionKind = "uid"
	// NSSCollisionKindUserName is two accounts with one name (posix_users).
	NSSCollisionKindUserName NSSCollisionKind = "user_name"
	// NSSCollisionKindGID is two groups with one gid (posix_groups).
	NSSCollisionKindGID NSSCollisionKind = "gid"
	// NSSCollisionKindGroupName is two groups with one name (posix_groups).
	NSSCollisionKindGroupName NSSCollisionKind = "group_name"
)
