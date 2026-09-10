package apitypes

import "time"

// UserRequestBody is the POST /api/certs/user request body. LocalUsername
// and LocalHostname are the requesting client's own OS identity — for a
// user cert there is no way to request one except via the local client, so
// this is who/where the request actually came from, not optional extra
// context.
type UserRequestBody struct {
	PublicKey     string `json:"public_key" binding:"required"`
	LocalUsername string `json:"local_username,omitempty"`
	LocalHostname string `json:"local_hostname,omitempty"`

	// The rest of the host context, the same set and the same trust as
	// PAMRequestBody's -- see that type for what each field means and
	// https://mnestor.github.io/ssoossh/internals/host-context/ for how far
	// each one travels. The client reads them off its own process and
	// machine (internal/hostinfo); every value is self-reported by an
	// unauthenticated caller and the approval page renders each as a claim.
	//
	// A user request carried only the two identity fields above until this
	// existed, so an approver deciding a `ssh login` saw two strings where
	// an approver deciding a `sudo` saw a dozen. All optional: an older
	// client that sends none of them still works, and a field the platform
	// cannot answer is simply absent.
	//
	// PAMService and Mode have no analogue here and are not sent. The
	// endpoint already says this is a user request, and there is no pam.d
	// line to have configured a mode in.
	//
	// RequestingUser is SUDO_USER, the analogue of PAM_RUSER: who invoked
	// the client, when that differs from the account it runs as.
	RequestingUser string `json:"requesting_user,omitempty"`
	// Process is the client's own argv ("ssoossh ssh login --force"),
	// the analogue of the PAM host process's command line. It is what
	// distinguishes an interactive login from a ProxyCommand invocation.
	Process string `json:"process,omitempty"`
	// TTY is the controlling terminal where the platform can name one.
	TTY string `json:"tty,omitempty"`
	// RemoteHost is the peer address when the client is itself running
	// inside an SSH session (SSH_CONNECTION), the analogue of PAM_RHOST.
	// Empty at a local terminal.
	RemoteHost string `json:"remote_host,omitempty"`
	// CallerUID, CallerGID, CallerPID and CallerPPID identify the client
	// process, for joining with the machine's own logs. Pointers so an
	// absent value is distinguishable from uid 0 or gid 0; the two ids are
	// absent on Windows, which has neither.
	CallerUID  *int64 `json:"caller_uid,omitempty"`
	CallerGID  *int64 `json:"caller_gid,omitempty"`
	CallerPID  *int64 `json:"caller_pid,omitempty"`
	CallerPPID *int64 `json:"caller_ppid,omitempty"`
	// MachineID is a stable per-install identifier: /etc/machine-id on
	// Linux, kern.uuid on macOS, kern.hostuuid on FreeBSD, MachineGuid on
	// Windows.
	MachineID string `json:"machine_id,omitempty"`
	// OS is the platform as the machine describes itself.
	OS string `json:"os,omitempty"`
	// ClientPolicy is what the requesting machine's administrative policy
	// locks, so an approver can tell a user's own opt-out from a rule
	// imposed on them. Optional, and absent from an older client.
	ClientPolicy *ClientPolicy `json:"client_policy,omitempty"`
	// Client names the implementation and version ("ssoossh/1.2.3"), which
	// is what tells this client and pam_ssoossh apart in a log.
	Client string `json:"client,omitempty"`
	// ClientTime is the machine's own clock when it built the request, so
	// skew against the server is visible before it breaks a login.
	ClientTime *time.Time `json:"client_time,omitempty"`
	// TrustedCAFingerprints is the SHA256 fingerprint of the CA public key
	// this client has *pinned* (the `capubkey` setting), in OpenSSH form.
	// Sent only when it was pinned: a key the client fetched from this same
	// server says nothing the server does not already know, and reporting
	// one as trust would be circular.
	TrustedCAFingerprints []string `json:"trusted_ca_fingerprints,omitempty"`

	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// ServiceEnrollRequestBody is the POST /api/certs/service/enroll request
// body. PublicKey may be operator-supplied (BYO key, possibly HSM/PKCS#11/
// encrypted file — the server never sees the private half) or
// client-generated (see
// https://mnestor.github.io/ssoossh/internals/design-brief/, "Service
// enrollment").
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

	// The rest of the host context, same trust as the four above: every value is
	// what the module read off its own process and machine, and the approval
	// page renders each as a claim. They exist so an approver of a `sudo` can
	// see which command is asking, on which machine, invoked by whom, and so the
	// audit line joins against the host's own auditd or journal. See
	// https://mnestor.github.io/ssoossh/internals/host-context/.
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
	// CallerGID is the process's group, added with the Go client and
	// accepted here so one column and one audit key mean the same thing
	// whichever implementation sent the request; the C module does not
	// report it yet.
	CallerUID  *int64 `json:"caller_uid,omitempty"`
	CallerGID  *int64 `json:"caller_gid,omitempty"`
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
// https://mnestor.github.io/ssoossh/concepts/console-flow/.
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
	CallerGID             *int64     `json:"caller_gid,omitempty"`
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

// ApproveResponse is POST /api/certs/requests/:id/approve's response body. It
// does not carry the certificate — approval only queues a signing job (see
// https://mnestor.github.io/ssoossh/internals/architecture/); the certificate
// itself is delivered later over the client's own SSE connection
// (CreateRequestResponse's EventsURL), not returned here to the approving
// browser.
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

// ClientPolicy is the administrative policy in force on the machine making
// a request — MDM managed preferences on macOS, Group Policy on Windows, or
// an enforce file — reported so the server can say why an option is missing
// rather than only that it is.
//
// # This is a claim, not a control
//
// It is self-reported by an unauthenticated caller, exactly like the host
// context beside it, and it is deliberately advisory: it narrows what an
// approver is offered and explains the gap, and it never refuses issuance.
//
// It cannot be otherwise. Assertions may only narrow — a client that could
// widen by claiming a policy would gain privilege by lying — and a client
// that wants the option simply omits the assertion, so nothing here binds a
// caller who does not wish to be bound. Anyone can also call the API
// directly and send none of it.
//
// What it buys is accuracy for the honest case, which is the common one: an
// approver is not offered a toggle that grants something the requesting
// machine will refuse to honour, and the page can say which rule accounts
// for a missing extension. Treat it as documentation of the request, never
// as authorization.
type ClientPolicy struct {
	// ForbiddenExtensions is forbidden_certificate_extensions as the
	// requesting machine has it. The client has already subtracted these
	// from what it asked for; sending them says why they are absent.
	ForbiddenExtensions []string `json:"forbidden_extensions,omitempty"`

	// FIPS reports that the machine is held to FIPS-approved algorithms by
	// policy. A pointer so "policy is silent" stays distinct from "policy
	// says false".
	FIPS *bool `json:"fips,omitempty"`
}
