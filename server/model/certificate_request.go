package model

import "time"

// CertificateRequest represents a client's in-flight ask for a user, host,
// or service certificate: created when the client asks (`ssh login`,
// `host sign`, `service enroll`), resolved when a user approves/denies in
// the web UI or it times out. Approving a CertificateTypeService request
// creates an Enrollment rather than issuing a certificate immediately — see
// https://mnestor.github.io/ssoossh/internals/design-brief/, "Service enrollment".
//
// The SSE endpoint the client is waiting on watches a request by ID.
// TODO: the pub/sub or channel-based broker backing that watch is not yet
// designed — see server/controller/certrequests.go's SSE handler stub.
type CertificateRequest struct {
	ID string `gorm:"column:id;primaryKey"`

	// Type carries a CHECK constraint mirroring the migration's — see
	// model.Certificate.Type for why the tag is duplicated there.
	Type CertificateType `gorm:"column:type;check:chk_certificate_requests_type,type IN ('user','service','pam','console')"`

	// UserID is set once the requester authenticates via OIDC. Absent for
	// an unauthenticated initial "host sign" ask, TODO: confirm host sign
	// requires OIDC login before or after the request row is created.
	UserID *string `gorm:"column:user_id"`

	PublicKey string `gorm:"column:public_key"`

	// Username is set only for CertificateTypePAM and
	// CertificateTypeConsole requests: the local account the PAM module is
	// authenticating (e.g. who is running `sudo`, or the account typed at
	// the `login:` prompt). It is context, not authority. An unauthenticated client
	// chooses it, so it reaches the approval page and the audit record and
	// stops there; the issued certificate's principals are accounts the
	// approver holds and selects (see service.newCertTypePolicies,
	// localAuthPrincipals). Whether those principals authorize this account
	// is the host's decision, made by pam_ssoossh's check 3: an exact match
	// against the account, or a match through its local principals-map. It
	// used to become the certificate's principal directly; see
	// docs/proposals/pam-principal-source.md for why that changed.
	Username string `gorm:"column:username"`

	// RequestedOptions is JSON-encoded. Server config (config.CertificateOptions)
	// is the outer bound — the web UI narrows or adjusts, never widens (see
	// https://mnestor.github.io/ssoossh/internals/invariants/).
	RequestedOptions string `gorm:"column:requested_options"`

	// SourceIP is one of the lifetime-policy signals (see
	// https://mnestor.github.io/ssoossh/internals/design-brief/ "Certificate
	// lifetime policy" — client-supplied source addresses are unverified input
	// and need a policy ceiling).
	SourceIP string `gorm:"column:source_ip"`

	// LocalUsername and LocalHostname are set only for CertificateTypeUser
	// requests: the OS user and hostname of the client that made the
	// request. For a user cert there is no way to request one except via
	// the local client, so local_user@host is the requester identity, not
	// optional extra context.
	LocalUsername string `gorm:"column:local_username"`
	LocalHostname string `gorm:"column:local_hostname"`

	// ServiceAccount is set only for CertificateTypeService requests: the
	// service account the certificate is for, selected during approval.
	// This closes the schema gap where a service enrollment request had no
	// link to which specific account was being enrolled.
	ServiceAccount string `gorm:"column:service_account"`

	// SerialNumber is the pre-allocated certificate serial for user/PAM
	// requests, set at approval time before signing. Null for service
	// enrollments (they don't produce certificates at approval time) and
	// host requests (not yet supported). Pre-allocation ensures the serial
	// is available to persist at resolution without waiting for the signer.
	// This avoids burning serials on signing failures.
	SerialNumber *uint64 `gorm:"column:serial_number"`

	// Status carries a CHECK constraint mirroring the migration's. Every
	// transition is a guarded UPDATE ... WHERE status = ?, so a value
	// outside the set would strand the row: no guarded update would match
	// it again and the sweep would never see it. The constraint makes that
	// a failed write rather than a silently unreachable request.
	Status     CertificateRequestStatus `gorm:"column:status;check:chk_certificate_requests_status,status IN ('pending','signing','approved','enrolled','denied','expired','failed')"`
	CreatedAt  time.Time                `gorm:"column:created_at"`
	ResolvedAt *time.Time               `gorm:"column:resolved_at"`

	// EnrollmentToken is set when Status is CertificateRequestStatusEnrolled
	// (CertificateTypeService only) — the code `service retrieve` presents
	// to redeem a certificate later. Empty for user/host requests.
	EnrollmentToken string `gorm:"column:enrollment_token"`

	// FailureReason explains a CertificateRequestStatusFailed row: either
	// the signer's error, or that the invalidation sweep found it stranded
	// (see https://mnestor.github.io/ssoossh/internals/architecture/). For operators
	// reading the database — it isn't returned over the API.
	FailureReason string `gorm:"column:failure_reason"`

	// ClaimTokenHash binds the /approve/<id> page to the first browser that
	// fetched it: hex SHA-256 of the claim cookie's value set on that first
	// GET (never the value itself, so a database read cannot mint a working
	// cookie). Nil means the page has not been opened yet. This is the
	// browser-level binding; UserID above is the separate identity-level
	// binding made on the first authenticated touch. See
	// CertRequestService.ClaimApprovalPage.
	ClaimTokenHash *string `gorm:"column:claim_token_hash"`

	// ClaimedAt and ClaimUserAgent exist for ClaimApprovalPage's
	// cookie-blocked heuristic (same user agent, cookieless, shortly after
	// the claim means a browser refusing cookies rather than a second
	// client) and for mismatch logging.
	ClaimedAt      *time.Time `gorm:"column:claimed_at"`
	ClaimUserAgent string     `gorm:"column:claim_user_agent"`

	// UserCode is the short code a console displays for a human to type
	// into the web UI, in its normalized form (see
	// service.NormalizeUserCode). Set only for CertificateTypeConsole
	// requests, empty for every other type, and unique among rows that are
	// still approvable — a collision would let one approver's typed code
	// resolve to a stranger's request.
	//
	// It is a lookup key for an already-authenticated approver, never a
	// capability: resolving one requires a session, and it is never
	// returned to an unauthenticated caller, written to an SSE payload, or
	// recorded in an audit Detail map. See
	// docs/proposals/console-login-pam.md, "The code is not a capability".
	UserCode string `gorm:"column:user_code"`

	// Hostname, PAMService, TTY and RemoteHost are the console context an
	// approver needs to tell a login they started from one an attacker
	// did: which machine, which PAM service (login, gdm, sddm), which
	// terminal, and — for a request claiming to be a console login —
	// whether PAM_RHOST says it is not one.
	//
	// Every one of them is self-reported by an unauthenticated caller and
	// must be presented as such. They are bounded on the way in
	// (see service.maxContextFieldLen) so a caller cannot write arbitrary
	// volume into the table, the same reasoning as claimUserAgentMaxLen.
	// Set for CertificateTypePAM and CertificateTypeConsole requests.
	Hostname   string `gorm:"column:hostname"`
	PAMService string `gorm:"column:pam_service"`
	TTY        string `gorm:"column:tty"`
	RemoteHost string `gorm:"column:remote_host"`

	// The rest of the host context a PAM or console module reports, with
	// the same trust and the same bound as the four above (see
	// apitypes.PAMRequestBody for each field's meaning). Set for
	// CertificateTypePAM and CertificateTypeConsole requests; the numeric
	// ones are pointers because uid 0 is a real value and NULL means "not
	// reported". TrustedCAFingerprints is a JSON-encoded []string.
	RequestingUser        string     `gorm:"column:requesting_user"`
	Process               string     `gorm:"column:process"`
	CallerUID             *int64     `gorm:"column:caller_uid"`
	CallerPID             *int64     `gorm:"column:caller_pid"`
	CallerPPID            *int64     `gorm:"column:caller_ppid"`
	MachineID             string     `gorm:"column:machine_id"`
	OS                    string     `gorm:"column:os"`
	Client                string     `gorm:"column:client"`
	ClientMode            string     `gorm:"column:client_mode"`
	ClientTime            *time.Time `gorm:"column:client_time"`
	TrustedCAFingerprints string     `gorm:"column:trusted_ca_fingerprints"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (CertificateRequest) TableName() string { return "certificate_requests" }

// ReportedIdentity returns the account and host the request claimed to
// come from, whichever columns hold them for this type: Username/Hostname
// for a PAM or console request, LocalUsername/LocalHostname for a user
// one. Both pairs mean the same thing, "who and where", and every consumer
// that wants it should come through here rather than pick a pair, so a
// user-type event never reads the PAM columns and gets empty strings.
func (r CertificateRequest) ReportedIdentity() (username, hostname string) {
	switch r.Type {
	case CertificateTypePAM, CertificateTypeConsole:
		return r.Username, r.Hostname
	default:
		return r.LocalUsername, r.LocalHostname
	}
}
