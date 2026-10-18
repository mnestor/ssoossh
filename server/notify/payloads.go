package notify

import "time"

// Reader is the per-recipient half of every payload, embedded in each one.
// The rest of a payload describes the event and is the same for everyone it
// reaches; this describes the person reading it, so it is set at delivery,
// once per recipient, and never travels on the queue.
type Reader struct {
	// Advanced is true when the recipient chose the advanced email detail
	// level. Templates branch on it: the simplified message keeps what the
	// reader needs to recognise the event and act, and the advanced one adds
	// the ids and options an expert compares.
	Advanced bool `json:"-"`
}

// SetAdvanced records the recipient's email detail level. Promoted to every
// payload, so delivery can set it without knowing the payload's type.
func (r *Reader) SetAdvanced(advanced bool) { r.Advanced = advanced }

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
	Reader

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
	Reader

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
	Reader

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
	Reader

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
	Reader

	// CertificateType is "user", "pam" or "console", matching the kind.
	// Carried in the payload as well as the kind so one shared template can
	// name it.
	CertificateType string `json:"certificate_type"`

	RequestID string `json:"request_id"`

	// CertificateID is the certificate's own record, what the email links
	// to (/certs/<id>). Empty when the record could not be read back, in
	// which case the link falls back to the certificate history.
	CertificateID string `json:"certificate_id,omitempty"`

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

// AdminAutoDisabled is the payload for KindAdminAutoDisabled: why the
// directory sync disabled an admin's account, when, and what to look at in
// the directory before asking for it back.
//
// It deliberately carries no group names. Who counts as an admin is decided
// from the login the account last made, and the message is about the
// account, not about the role that made it worth telling.
type AdminAutoDisabled struct {
	Reader

	Username   string    `json:"username"`
	DisabledAt time.Time `json:"disabled_at"`

	// MissingSince opens the window the threshold measured, and MissingFor
	// is how long it had run; DisableAfter is the threshold itself. All
	// three, so the reader can tell a long-gone entry from one that only
	// just crossed the line.
	MissingSince time.Time     `json:"missing_since"`
	MissingFor   time.Duration `json:"missing_for"`
	DisableAfter time.Duration `json:"disable_after"`

	// DirectoryDN and BaseDN are where to look: the entry's last known
	// location and the subtree the sync searches.
	DirectoryDN string `json:"directory_dn,omitempty"`
	BaseDN      string `json:"base_dn,omitempty"`

	// Reenable says whether the entry coming back is enough on its own, or
	// whether a person has to clear the disable.
	Reenable bool `json:"reenable,omitempty"`

	SupportEmail string `json:"support_email,omitempty"`
	ServerURL    string `json:"server_url"`
}

// The NSS fleet key payloads below describe one fleet key from
// nss_keys (see model.NSSKey). None of them carries a key secret, current
// or previous, and none may: mail is stored, forwarded and read on devices
// the server knows nothing about, and a secret is shown exactly once, when
// the key is created or rotated.

// NSSKeyExpiring is the payload for KindNSSKeyExpiring: the reminder that a
// fleet key is about to stop being accepted, and hosts presenting it will
// stop resolving accounts through it.
type NSSKeyExpiring struct {
	Reader

	KeyID   string `json:"key_id"`
	KeyName string `json:"key_name"`

	// Description and GroupName are the key's own descriptive fields, empty
	// when unset. GroupName is the group whose members manage the key.
	Description string `json:"description,omitempty"`
	GroupName   string `json:"group_name,omitempty"`

	// ExpiresAt is when the key stops being accepted. The expiry cannot be
	// moved, so a replacement key has to reach the hosts before this.
	ExpiresAt time.Time `json:"expires_at"`

	// Daily is true once the key is inside its final week and reminders have
	// moved from weekly to daily, so the message can say when the next one
	// comes.
	Daily bool `json:"daily"`

	ServerURL string `json:"server_url"`
}

// NSSKeyRotationCleanup is the payload for KindNSSKeyRotationCleanup: the
// digest sent after a rotation while some hosts still present the previous
// secret. Each address is classified by its most recent lookup.
type NSSKeyRotationCleanup struct {
	Reader

	KeyID   string `json:"key_id"`
	KeyName string `json:"key_name"`

	// Generation is the current generation, the one hosts should present.
	Generation int `json:"generation"`

	// RotatedAt is when the rotation happened. PreviousExpiresAt is when the
	// previous secret stops working, equal to RotatedAt when
	// nss.key_rotation_grace is zero.
	RotatedAt         time.Time `json:"rotated_at"`
	PreviousExpiresAt time.Time `json:"previous_expires_at"`

	// Switched counts addresses whose last lookup presented the current
	// secret. NotSwitched counts those whose last lookup presented the
	// previous one. Gone counts addresses seen before the rotation and not
	// since, which are often hosts that have been retired.
	Switched    int `json:"switched"`
	NotSwitched int `json:"not_switched"`
	Gone        int `json:"gone"`

	// NotSwitchedAddresses lists up to 50 not_switched addresses, most
	// recently seen first. MoreNotSwitched is how many more there are beyond
	// that list, and is omitted when there are none.
	NotSwitchedAddresses []string `json:"not_switched_addresses"`
	MoreNotSwitched      int      `json:"more_not_switched,omitempty"`

	ServerURL string `json:"server_url"`
}

// NSSKeyExpiredAttempt is the payload for KindNSSKeyExpiredAttempt: a host
// presented a key after it expired. The request was refused, and the
// attempt is attributable to the key and to the address it came from.
type NSSKeyExpiredAttempt struct {
	Reader

	KeyID   string `json:"key_id"`
	KeyName string `json:"key_name"`

	// SourceIP is the connection's address as the server observed it.
	SourceIP    string    `json:"source_ip"`
	AttemptedAt time.Time `json:"attempted_at"`

	// ExpiredAt is when the key stopped being accepted, which with
	// AttemptedAt separates a host that has been failing for a while from
	// one that only just started.
	ExpiredAt time.Time `json:"expired_at"`

	ServerURL string `json:"server_url"`
}

// NSSKeyRevokedAttempt is the payload for KindNSSKeyRevokedAttempt: a host
// presented a key after it was revoked. Otherwise the same as
// NSSKeyExpiredAttempt.
type NSSKeyRevokedAttempt struct {
	Reader

	KeyID   string `json:"key_id"`
	KeyName string `json:"key_name"`

	// SourceIP is the connection's address as the server observed it.
	SourceIP    string    `json:"source_ip"`
	AttemptedAt time.Time `json:"attempted_at"`

	// RevokedAt is when the key was revoked, which with AttemptedAt says
	// whether a host is still using a key that has been dead for a while.
	RevokedAt time.Time `json:"revoked_at"`

	ServerURL string `json:"server_url"`
}

// NSSKeyRotated is the payload for KindNSSKeyRotated, sent at once when a
// key is rotated: who did it, when, and until when the previous secret
// keeps working.
type NSSKeyRotated struct {
	Reader

	KeyID   string `json:"key_id"`
	KeyName string `json:"key_name"`

	// Generation is the new current generation.
	Generation int       `json:"generation"`
	RotatedAt  time.Time `json:"rotated_at"`

	// RotatedByUsername is the account that rotated the key. RotatedByName
	// is its human-readable name, display only, and empty when none was
	// captured.
	RotatedByUsername string `json:"rotated_by_username"`
	RotatedByName     string `json:"rotated_by_name,omitempty"`

	// PreviousExpiresAt is when the previous secret stops working. Equal to
	// RotatedAt when nss.key_rotation_grace is zero.
	PreviousExpiresAt time.Time `json:"previous_expires_at"`

	ServerURL string `json:"server_url"`
}

// NSSKeyRevoked is the payload for KindNSSKeyRevoked, sent at once when a
// key is revoked: who did it and when.
type NSSKeyRevoked struct {
	Reader

	KeyID   string `json:"key_id"`
	KeyName string `json:"key_name"`

	RevokedAt time.Time `json:"revoked_at"`

	// RevokedByUsername is the account that revoked the key. RevokedByName
	// is its human-readable name, display only, and empty when none was
	// captured.
	RevokedByUsername string `json:"revoked_by_username"`
	RevokedByName     string `json:"revoked_by_name,omitempty"`

	ServerURL string `json:"server_url"`
}
