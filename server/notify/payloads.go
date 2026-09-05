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

// ServiceEnrollmentExpiring is the payload for
// KindServiceEnrollmentExpiring: the follow-up the "created" message
// promises but has no way to send.
//
// By the time the expiry date matters, the terminal that displayed the code
// is long gone and the cron job is the only thing that remembers the
// enrollment exists — by failing. Everything here is what the recipient
// needs to decide between re-enrolling and letting it lapse, which is why
// FirstRedeemedAt is included: a code that has never been redeemed is
// usually a job that was never finished, and a different decision from one
// that has been running for months.
//
// No code, same as every other kind. Re-enrolling means running
// `ssoossh service enroll` again, not reusing anything in this message.
type ServiceEnrollmentExpiring struct {
	ServiceAccount string   `json:"service_account"`
	RequestID      string   `json:"request_id,omitempty"`
	EnrollmentID   string   `json:"enrollment_id"`
	KeyID          string   `json:"key_id"`
	Principals     []string `json:"principals"`

	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	PublicKeyType        string `json:"public_key_type"`

	// FirstRedeemedAt is when the code was first redeemed, zero if it never
	// was. The first rather than the last because it is on the enrollment
	// row: the reminder sweep reads one table, and asking it to aggregate
	// the retrieval log for every expiring row would be a join per reminder
	// for a detail the recipient can look up.
	FirstRedeemedAt time.Time `json:"first_redeemed_at,omitempty"`

	// Daily is true once the code is inside the final week and reminders
	// have moved from weekly to daily, so the message can say when the
	// next one comes rather than pretending to be the only one.
	Daily bool `json:"daily"`

	CodeExpiresAt time.Time `json:"code_expires_at"`
	ServerURL     string    `json:"server_url"`
}

// ServiceEnrollmentExpiredAttempt is the payload for
// KindServiceEnrollmentExpiredAttempt.
//
// `service retrieve` answers an expired code exactly like an unknown one —
// the caller holds a dead capability either way — but the server has
// already loaded the row by then, so the attempt is fully attributable.
// Either a forgotten job is failing on schedule or someone is replaying a
// credential that should no longer exist, and both are things the account's
// holders want to hear about.
//
// Attempts with genuinely unknown codes send nothing: there is no row, so
// there is no one to tell.
type ServiceEnrollmentExpiredAttempt struct {
	ServiceAccount string   `json:"service_account"`
	RequestID      string   `json:"request_id,omitempty"`
	EnrollmentID   string   `json:"enrollment_id"`
	KeyID          string   `json:"key_id"`
	Principals     []string `json:"principals"`

	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	PublicKeyType        string `json:"public_key_type"`

	SourceIP    string    `json:"source_ip"`
	AttemptedAt time.Time `json:"attempted_at"`

	// CodeExpiredAt is when the code stopped being redeemable, which with
	// AttemptedAt is what separates "expired an hour ago, nobody noticed"
	// from "expired last quarter and something is still trying".
	CodeExpiredAt time.Time `json:"code_expired_at"`

	ServerURL string `json:"server_url"`
}

// CertificateIssued is the payload for KindUserCertificateIssued,
// KindPAMCertificateIssued and KindConsoleCertificateIssued: the "was this
// you?" message.
//
// The requester was present for every flow — approving in a browser,
// typing a password at a PAM prompt, or typing a code from a console into
// the web UI — so on the happy path this confirms
// what the reader already knows. Its value is the unhappy path: a
// certificate minted by a session they do not recognize, from an address
// they were never at. That is why SourceIP and the granted option set are
// here and not just the identity of the certificate.
//
// One struct for three kinds because they describe the same object; the
// kinds are separate so the preferences can be.
type CertificateIssued struct {
	// CertificateType is "user", "pam" or "console", matching the kind.
	// Carried in the payload as well as the kind so one shared template can
	// name it.
	CertificateType string `json:"certificate_type"`

	RequestID string `json:"request_id"`

	KeyID      string   `json:"key_id"`
	Principals []string `json:"principals"`
	Serial     uint64   `json:"serial"`

	PublicKeyFingerprint string `json:"public_key_fingerprint"`

	// LocalUsername and LocalHostname are the account and machine the
	// client reported at request time: for a user certificate the OS user
	// and host the client ran on, for PAM whose `sudo` this authorized, for
	// console which account was typed at which machine's login prompt.
	// Client-reported and therefore not evidence, but they are what makes
	// the message recognizable to the person who was there.
	LocalUsername string `json:"local_username,omitempty"`
	LocalHostname string `json:"local_hostname,omitempty"`

	SourceIP  string    `json:"source_ip"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`

	Extensions      []string `json:"extensions,omitempty"`
	ForceCommand    string   `json:"force_command,omitempty"`
	SourceAddresses []string `json:"source_addresses,omitempty"`

	ServerURL string `json:"server_url"`
}
