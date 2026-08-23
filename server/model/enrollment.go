package model

import "time"

// Enrollment binds an approved service-certificate option set to a public
// key. Created when a CertificateTypeService CertificateRequest is
// approved. After that, `service retrieve` calls present only Code — the
// server never re-accepts a public key for an existing enrollment, so a
// stolen code can't be paired with an attacker's keypair (see
// docs/dev/ssoossh-context.md, "Service enrollment").
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

	// CertificateRequestID links back to the approved request, keeping
	// certificates issued at retrieval time on the same audit chain as the
	// approval decision.
	CertificateRequestID *string `gorm:"column:certificate_request_id"`

	UserID    string    `gorm:"column:user_id"` // who approved this enrollment
	CreatedAt time.Time `gorm:"column:created_at"`
	ExpiresAt time.Time `gorm:"column:expires_at"`

	// RedeemedAt is set on first successful `service retrieve`. Enrollment
	// codes are reusable until ExpiresAt — this timestamp is audit detail,
	// not a single-use gate. Per-redemption history is in
	// EnrollmentRetrieval.
	RedeemedAt *time.Time `gorm:"column:redeemed_at"`
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
