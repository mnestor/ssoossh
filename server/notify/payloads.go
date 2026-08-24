package notify

import "time"

// ServiceEnrollmentCreated is the payload for
// KindServiceEnrollmentCreated: everything `ssoossh service enroll` reports
// to the operator's terminal, minus the code itself.
//
// The code is deliberately absent and must stay absent. It is a bearer
// credential that mints certificates unattended, and mail is stored,
// forwarded, indexed, and read on devices the server knows nothing about —
// the terminal that ran `service enroll` is the only place it is ever
// shown. Everything here is the surrounding detail needed to recognize the
// enrollment, audit it, and set the job up; none of it is sufficient to
// redeem anything.
//
// Every exported field here is a documented template variable — see the
// Fields list on this kind's Definition, which a test keeps in step with
// this struct.
type ServiceEnrollmentCreated struct {
	ServiceAccount string   `json:"service_account"`
	RequestID      string   `json:"request_id"`
	EnrollmentID   string   `json:"enrollment_id"`
	KeyID          string   `json:"key_id"`
	Principals     []string `json:"principals"`

	// PublicKeyFingerprint and PublicKeyType identify the enrolled key
	// without carrying it. The binding of code to key is the property the
	// enrollment design leans on hardest (a stolen code cannot be paired
	// with an attacker's keypair), so naming the key is what lets the
	// recipient tell a legitimate enrollment from one they did not make.
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	PublicKeyType        string `json:"public_key_type"`

	Extensions      []string `json:"extensions,omitempty"`
	ForceCommand    string   `json:"force_command,omitempty"`
	SourceAddresses []string `json:"source_addresses,omitempty"`
	NoTouchRequired bool     `json:"no_touch_required,omitempty"`

	RequestSourceIP    string    `json:"request_source_ip"`
	ApprovedAt         time.Time `json:"approved_at"`
	ApprovedByUsername string    `json:"approved_by_username"`

	// CodeExpiresAt bounds the code; CertificateLifetime bounds each
	// certificate it produces, measured from each redemption. They are
	// separate spans on purpose — see config.CertOptionsService.
	CodeExpiresAt       time.Time     `json:"code_expires_at"`
	CertificateLifetime time.Duration `json:"certificate_lifetime"`

	ServerURL string `json:"server_url"`
}

// ServiceEnrollmentRedeemed is the payload for
// KindServiceEnrollmentRedeemed, sent to the approving user on every
// redemption of one of their codes.
//
// Failed redemptions are reported too (Succeeded false): a code that
// validated but could not be signed is exactly the case an operator wants
// to hear about, and staying quiet about it would make this notification a
// success log rather than an alarm.
type ServiceEnrollmentRedeemed struct {
	ServiceAccount string `json:"service_account"`
	RequestID      string `json:"request_id,omitempty"`
	EnrollmentID   string `json:"enrollment_id"`
	RetrievalID    string `json:"retrieval_id"`

	SourceIP    string    `json:"source_ip"`
	RetrievedAt time.Time `json:"retrieved_at"`

	CertificateSerial    uint64    `json:"certificate_serial"`
	CertificateExpiresAt time.Time `json:"certificate_expires_at"`
	KeyID                string    `json:"key_id"`
	Principals           []string  `json:"principals"`

	Succeeded bool `json:"succeeded"`

	// FirstRedemption distinguishes the redemption that proves a new job
	// works from the thousands that follow it on a cron schedule.
	FirstRedemption bool `json:"first_redemption"`

	CodeExpiresAt time.Time `json:"code_expires_at"`
	ServerURL     string    `json:"server_url"`
}
