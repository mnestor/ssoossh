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
// there. The certificate's principals are the approver's own accounts, and
// the host's principals-map decides whether they authorize that account.
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
