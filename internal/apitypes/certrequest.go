package apitypes

import "time"

// UserRequestBody is the POST /api/certs/user request body. LocalUsername
// and LocalHostname are the requesting client's own OS identity — for a
// user cert there is no way to request one except via the local client, so
// this is who/where the request actually came from, not optional extra
// context.
type UserRequestBody struct {
	PublicKey        string           `json:"public_key" binding:"required"`
	LocalUsername    string           `json:"local_username,omitempty"`
	LocalHostname    string           `json:"local_hostname,omitempty"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// ServiceEnrollRequestBody is the POST /api/certs/service/enroll request
// body. PublicKey may be operator-supplied (BYO key, possibly HSM/PKCS#11/
// encrypted file — the server never sees the private half) or
// client-generated (see docs/internals/design-brief.md, "Service enrollment").
type ServiceEnrollRequestBody struct {
	PublicKey        string           `json:"public_key" binding:"required"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// PAMRequestBody is the POST /api/certs/pam request body. Username is the
// local account the PAM module is authenticating (e.g. who is running
// `sudo`); it reaches the approval page and the audit record and stops
// there. The certificate's principals are accounts the approver holds and
// selects; the module on the host matches them against that account,
// directly or through its principals-map.
type PAMRequestBody struct {
	PublicKey string `json:"public_key" binding:"required"`
	Username  string `json:"username" binding:"required"`

	// The context an approver needs to tell a request they caused from one
	// they did not: which machine, through which PAM service, at which
	// terminal, and — since a real console has no remote host — whether
	// PAM_RHOST says this did not come from a console at all.
	//
	// Every one of these is self-reported by an unauthenticated caller and
	// is displayed as a claim, never as a fact. Optional, so a client that
	// does not read PAM items still works.
	Hostname   string `json:"hostname,omitempty"`
	PAMService string `json:"pam_service,omitempty"`
	TTY        string `json:"tty,omitempty"`
	RemoteHost string `json:"remote_host,omitempty"`

	// The rest of the host context, same trust as the four above: every
	// value is what the module read off its own process and machine, and
	// the approval page renders each as a claim. They exist so an
	// approver of a `sudo` can see which command is asking, on which
	// machine, invoked by whom, and so the audit line joins against the
	// host's own auditd or journal. See docs/internals/host-context.md.
	//
	// RequestingUser is PAM_RUSER: who invoked the service, as opposed to
	// Username, the account being authenticated. Under `su` or sudo's
	// targetpw they differ.
	RequestingUser string `json:"requesting_user,omitempty"`
	// Process is the PAM host process's command line ("sudo -i",
	// "sudo systemctl restart nginx"), read from /proc/self/cmdline where
	// the platform has it. Empty where it does not.
	Process string `json:"process,omitempty"`
	// CallerUID, CallerPID and CallerPPID identify the process on the host,
	// for joining with the host's own logs. Pointers so an absent value is
	// distinguishable from uid 0 or pid 0.
	CallerUID  *int64 `json:"caller_uid,omitempty"`
	CallerPID  *int64 `json:"caller_pid,omitempty"`
	CallerPPID *int64 `json:"caller_ppid,omitempty"`
	// MachineID is a stable per-install identifier (/etc/machine-id or
	// kern.hostuuid), so a host is still recognisable after a rename and
	// two hosts claiming one name are distinguishable.
	MachineID string `json:"machine_id,omitempty"`
	// OS is the platform as the host describes itself: os-release
	// PRETTY_NAME followed by uname -s and -r.
	OS string `json:"os,omitempty"`
	// Client names the module and its version ("pam_ssoossh-c/0.3.0"),
	// which is what tells the two module implementations apart in a log.
	Client string `json:"client,omitempty"`
	// Mode is the module's configured mode argument as written in pam.d
	// ("auto", "sudo", "console"), not the route it resolved to; the
	// endpoint already says that. Together they explain why a request
	// arrived as a console one.
	Mode string `json:"mode,omitempty"`
	// ClientTime is the host's own clock when it built the request, so
	// skew against the server is visible before it fails a login.
	ClientTime *time.Time `json:"client_time,omitempty"`
	// TrustedCAFingerprints are the SHA256 fingerprints of the keys in the
	// module's trusted-ca-file, in OpenSSH form. The module will reject a
	// certificate signed by any other key, so the server can warn the
	// approver before that happens rather than after.
	TrustedCAFingerprints []string `json:"trusted_ca_fingerprints,omitempty"`

	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// ConsoleRequestBody is the POST /api/certs/console request body: an
// interactive console login on a machine with no browser in front of it.
//
// Identical in shape to PAMRequestBody, and deliberately a separate type
// and a separate endpoint. The certificate type decides the approval gate,
// the lifetime, the key ID and the approval budget, and a console session
// and a single `sudo` want different answers to all four — see
// docs/proposals/console-login-pam.md.
//
// The response carries a short code (CreateRequestResponse.UserCode) rather
// than expecting anyone to transcribe an approval URL: there is nothing to
// copy from a physical tty, a serial console or a BMC viewer.
type ConsoleRequestBody struct {
	PublicKey string `json:"public_key" binding:"required"`
	Username  string `json:"username" binding:"required"`

	// Same fields, same trust, as PAMRequestBody's — and they matter more
	// here, because a console certificate authorizes a whole session.
	Hostname   string `json:"hostname,omitempty"`
	PAMService string `json:"pam_service,omitempty"`
	TTY        string `json:"tty,omitempty"`
	RemoteHost string `json:"remote_host,omitempty"`

	// The rest of the host context; see PAMRequestBody for each field.
	RequestingUser        string     `json:"requesting_user,omitempty"`
	Process               string     `json:"process,omitempty"`
	CallerUID             *int64     `json:"caller_uid,omitempty"`
	CallerPID             *int64     `json:"caller_pid,omitempty"`
	CallerPPID            *int64     `json:"caller_ppid,omitempty"`
	MachineID             string     `json:"machine_id,omitempty"`
	OS                    string     `json:"os,omitempty"`
	Client                string     `json:"client,omitempty"`
	Mode                  string     `json:"mode,omitempty"`
	ClientTime            *time.Time `json:"client_time,omitempty"`
	TrustedCAFingerprints []string   `json:"trusted_ca_fingerprints,omitempty"`

	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// CreateRequestResponse is what every create-request endpoint
// (UserRequestBody/ServiceEnrollRequestBody/PAMRequestBody's handlers)
// returns: the created request's ID plus two URLs — EventsURL for the
// client's own SSE connection to wait on the outcome, ApprovalURL for the
// human to open in a browser. Both are relative — the client already knows
// the server's base URL (it just POSTed to it) and prepends that itself
// for both, so the server doesn't need to know its own public base URL to
// build an absolute link.
type CreateRequestResponse struct {
	RequestID   string `json:"request_id" validate:"required"`
	EventsURL   string `json:"events_url" validate:"required"`
	ApprovalURL string `json:"approval_url" validate:"required"`

	// UserCode is the short code a human types into the web UI, grouped
	// for display ("K7M4-QP2X"). Console requests only; empty for every
	// other type, so no existing consumer changes.
	//
	// This is the only response it ever appears in. It is not in the SSE
	// stream, not in the request detail, and not in any audit record.
	UserCode string `json:"user_code,omitempty"`

	// VerificationURL is the page that accepts UserCode;
	// VerificationURLComplete embeds the code so a device with a camera or
	// a keyboard can skip the code box entirely. Relative, like the two
	// URLs above, and console requests only.
	//
	// Complete is kept short (/c/<code>, not /approve/<uuid>) because it is
	// what a QR code drawn in an 80x24 terminal has to encode.
	VerificationURL         string `json:"verification_url,omitempty"`
	VerificationURLComplete string `json:"verification_url_complete,omitempty"`

	// ExpiresAt is when this request stops being approvable, from its own
	// type's budget (cert_options.<type>.client_timeout, falling back to
	// cert_options.client_timeout). Populated for every type.
	//
	// A client bounds its wait by this rather than by a local guess, and a
	// console displays the time remaining from it. Without it, the client's
	// timeout and the server's budget are two numbers an operator keeps in
	// agreement by hand, and disagreement surfaces as a client still
	// waiting on a request the server already killed. RFC 8628 carries
	// expires_in for the same reason.
	ExpiresAt time.Time `json:"expires_at"`
}

// ApproveResponse is POST /api/certs/requests/:id/approve's response body.
// It does not carry the certificate — approval only queues a signing job
// (see docs/internals/signing-pipeline.md); the certificate itself is delivered
// later over the client's own SSE connection (CreateRequestResponse's
// EventsURL), not returned here to the approving browser.
type ApproveResponse struct {
	// Status is always "signing" today — included so the response shape
	// can carry more without a breaking change later.
	Status string `json:"status" validate:"required"`
}

// DenyResponse is the body of a successful deny. It carries the resulting
// status for symmetry with ApproveResponse, so both halves of the approval
// decision look the same on the wire.
type DenyResponse struct {
	Status string `json:"status" validate:"required"`
}
