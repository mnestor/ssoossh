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

	// RequestSourceIP is the requester's address; ApproverSourceIP is the
	// approver's. Two different questions, and on a message announcing a
	// credential that mints certificates unattended for months, the second
	// is the one that says whose session let it exist.
	RequestSourceIP    string    `json:"request_source_ip"`
	ApprovedAt         time.Time `json:"approved_at"`
	ApprovedByUsername string    `json:"approved_by_username"`
	// ApprovedByName is the approver as a person reads them — "Ada
	// Lovelace" rather than "alovelace". Display only, and empty when
	// nothing supplied a name; a template wanting a guaranteed value uses
	// ApprovedByUsername.
	ApprovedByName   string `json:"approved_by_name,omitempty"`
	ApprovedByEmail  string `json:"approved_by_email,omitempty"`
	ApproverSourceIP string `json:"approver_source_ip,omitempty"`

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

	// The rest of the host context the request reported, the same set the
	// approval page showed and every cert.* audit event carries (see
	// https://mnestor.github.io/ssoossh/internals/host-context/). Present
	// because this is the "was this you?" message: "a certificate was
	// issued" is not answerable, and "sudo systemctl restart nginx on
	// rack07, at pts/3, from a machine you have never used" is.
	//
	// Client-reported and therefore not evidence, exactly like
	// LocalUsername above. A template rendering any of it must present it
	// as a claim; the shipped ones say so in a line under the block.
	//
	// Every field is optional. A user certificate has no PAMService, a
	// local terminal has no RemoteHost, and a client that reported none of
	// it leaves the whole block empty.
	RequestingUser string `json:"requesting_user,omitempty"`
	Process        string `json:"process,omitempty"`
	TTY            string `json:"tty,omitempty"`
	RemoteHost     string `json:"remote_host,omitempty"`
	PAMService     string `json:"pam_service,omitempty"`
	MachineID      string `json:"machine_id,omitempty"`
	OS             string `json:"os,omitempty"`
	Client         string `json:"client,omitempty"`
	ClientMode     string `json:"client_mode,omitempty"`

	// The process on the reporting machine, for joining against its own
	// auditd or journal. Pointers so absent is distinguishable from uid 0,
	// and from a platform that has no such id at all.
	CallerUID  *int64 `json:"caller_uid,omitempty"`
	CallerGID  *int64 `json:"caller_gid,omitempty"`
	CallerPID  *int64 `json:"caller_pid,omitempty"`
	CallerPPID *int64 `json:"caller_ppid,omitempty"`

	SourceIP  string    `json:"source_ip"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`

	// Who approved the request, from where, and when.
	//
	// The most security-relevant thing in this message and the thing it
	// carried none of: a recipient could see that a certificate existed but
	// not who let it exist. On the unhappy path -- a session the reader does
	// not recognize -- the approver's address is the fact that says whether
	// their own account was used or someone else's.
	//
	// Empty on a certificate whose decision record has gone or predates
	// them, which the shipped templates render as "not recorded" rather
	// than as nobody.
	ApprovedByUsername string `json:"approved_by_username,omitempty"`
	// ApprovedByName is the approver as a person reads them. Display only,
	// and empty when nothing supplied a name — on the "was this you?"
	// message it is what turns an unfamiliar account name into a
	// recognizable colleague.
	ApprovedByName    string    `json:"approved_by_name,omitempty"`
	ApprovedByEmail   string    `json:"approved_by_email,omitempty"`
	ApproverSourceIP  string    `json:"approver_source_ip,omitempty"`
	ApproverUserAgent string    `json:"approver_user_agent,omitempty"`
	ApprovedAt        time.Time `json:"approved_at,omitempty"`

	Extensions      []string `json:"extensions,omitempty"`
	ForceCommand    string   `json:"force_command,omitempty"`
	SourceAddresses []string `json:"source_addresses,omitempty"`

	// NoTouchRequired completes the granted set. A certificate that waives
	// the hardware-key touch is a weaker credential than one that does not,
	// so its absence from this message was a gap in what the reader is
	// being asked to confirm.
	NoTouchRequired bool `json:"no_touch_required,omitempty"`

	ServerURL string `json:"server_url"`
}
