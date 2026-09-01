package model

import "time"

// Enrollment binds an approved service-certificate option set to a public
// key. Created when a CertificateTypeService CertificateRequest is
// approved. After that, `service retrieve` calls present only Code — the
// server never re-accepts a public key for an existing enrollment, so a
// stolen code can't be paired with an attacker's keypair (see
// docs/internals/design-brief.md, "Service enrollment").
type Enrollment struct {
	ID        string `gorm:"column:id;primaryKey"`
	Code      string `gorm:"column:code"`
	PublicKey string `gorm:"column:public_key"`

	// OptionSet is JSON-encoded, fixed at approval time.
	OptionSet string `gorm:"column:option_set"`

	// KeyID and Principals are likewise fixed at approval time
	// (evaluate-at-enrollment-time: retrieval never re-derives policy).
	// Principals is a JSON-encoded []string.
	KeyID      string `gorm:"column:key_id"`
	Principals string `gorm:"column:principals"`

	// ServiceAccount is the account this enrollment was approved for, and
	// the whole of its ownership: every user holding it owns this
	// enrollment (see docs/proposals/enrollment-group-ownership.md). It is
	// Principals' sole element, denormalized out of that JSON string so
	// ownership is an indexed query in both dialects rather than a
	// per-dialect JSON expression.
	//
	// Empty only on a row whose principals never parsed, which matches no
	// account and so is owned by nobody — visible to auditors, to no one
	// else.
	ServiceAccount string `gorm:"column:service_account"`

	// CertificateRequestID links back to the approved request, keeping
	// certificates issued at retrieval time on the same audit chain as the
	// approval decision.
	CertificateRequestID *string `gorm:"column:certificate_request_id"`

	// UserID is who approved this enrollment. Audit provenance only: it
	// grants nothing and is never moved. Ownership is ServiceAccount.
	UserID    string    `gorm:"column:user_id"`
	CreatedAt time.Time `gorm:"column:created_at"`

	// ExpiresAt bounds the *code*, not the certificates it produces: after
	// it, `service retrieve` stops redeeming. It comes from
	// cert_options.service.enrollment_duration.
	ExpiresAt time.Time `gorm:"column:expires_at"`

	// CertificateDurationSeconds is how long each certificate redeemed from
	// this enrollment is valid for, fixed at approval time along with KeyID
	// and Principals (evaluate-at-enrollment-time) but measured from each
	// redemption.
	//
	// A pointer so nil — a row written before the column existed, where
	// ExpiresAt served as both bounds — stays distinct from a stored zero,
	// which is an approval that computed a zero-length certificate and must
	// fail at the signer rather than inherit the code's window. See
	// EnrollmentService.Retrieve.
	CertificateDurationSeconds *int64 `gorm:"column:certificate_duration_seconds"`

	// RedeemedAt is set on first successful `service retrieve`. Enrollment
	// codes are reusable until ExpiresAt — this timestamp is audit detail,
	// not a single-use gate. Per-redemption history is in
	// EnrollmentRetrieval.
	RedeemedAt *time.Time `gorm:"column:redeemed_at"`

	// NotificationEmail overrides where notifications about this enrollment
	// go: set, it is the sole recipient; empty, they fan out to every holder
	// of ServiceAccount. It exists for the two cases fan-out cannot reach —
	// an account whose holders have never logged in, and a team alias that
	// should hear about the job instead of everyone who happens to hold the
	// account (see docs/proposals/notification-kinds-expansion.md).
	//
	// A set address sends ungated: with no single owning user there is no
	// principled person whose per-kind preference could gate it, and the
	// address is the account's own subscription, entered deliberately.
	NotificationEmail string `gorm:"column:notification_email"`

	// ExpiryReminderSentAt claims the one expiry reminder this enrollment
	// gets. Nil means unclaimed; the sweep takes it with a guarded UPDATE
	// and publishes only if that reports a row, so every instance can sweep
	// without any of them duplicating the send.
	//
	// Any path that moves ExpiresAt earlier must clear this, or a reminder
	// already sent for the old horizon suppresses the one that matters for
	// the new, closer one.
	ExpiryReminderSentAt *time.Time `gorm:"column:expiry_reminder_sent_at"`

	// LastExpiredAttemptNotifiedAt rate-limits the expired-attempt
	// notification. A cron job holding a dead code retries on schedule
	// forever, so this is claimed with a window rather than once: an attempt
	// notifies only when this is nil or older than the configured window.
	LastExpiredAttemptNotifiedAt *time.Time `gorm:"column:last_expired_attempt_notified_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (Enrollment) TableName() string { return "enrollments" }

// EnrollmentRetrieval logs one `service retrieve` redemption of an
// enrollment code. Codes are reusable until expiry, so an enrollment can
// have many of these; the approving user and auditors read them back to see
// when and from where the code was used. CertificateSerial links to the
// certificates audit row when signing succeeded (Succeeded true).
type EnrollmentRetrieval struct {
	ID           string `gorm:"column:id;primaryKey"`
	EnrollmentID string `gorm:"column:enrollment_id"`
	SourceIP     string `gorm:"column:source_ip"`

	// CertificateSerial is pre-allocated before signing is queued, same as
	// CertificateRequest.SerialNumber, so the row can exist before the
	// signer answers.
	CertificateSerial uint64 `gorm:"column:certificate_serial"`

	RetrievedAt time.Time `gorm:"column:retrieved_at"`

	// Succeeded is set once the signed certificate was delivered to the
	// caller; a false row records a redemption attempt that passed code
	// validation but failed at signing.
	Succeeded bool `gorm:"column:succeeded"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (EnrollmentRetrieval) TableName() string { return "enrollment_retrievals" }

// EnrollmentReassignment is an audit record of one enrollment ownership
// transfer, from when an enrollment had a single owner that could be moved.
//
// Historical and read-only: group ownership removed reassignment (see
// docs/proposals/enrollment-group-ownership.md), so no new rows are ever
// written. The existing ones are kept and still rendered — they record
// transfers that really happened, and dropping the table would erase that.
//
// The distinction between FromUserID and ReassignedByUserID is why they are
// both here: an owner moving their own code has them equal, an admin moving
// someone else's has them differ, and auditors read that to tell
// self-service from admin-initiated transfer.
type EnrollmentReassignment struct {
	ID                 string    `gorm:"column:id;primaryKey"`
	EnrollmentID       string    `gorm:"column:enrollment_id"`
	FromUserID         string    `gorm:"column:from_user_id"`
	ToUserID           string    `gorm:"column:to_user_id"`
	ReassignedByUserID string    `gorm:"column:reassigned_by_user_id"`
	ReassignedAt       time.Time `gorm:"column:reassigned_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (EnrollmentReassignment) TableName() string { return "enrollment_reassignments" }
