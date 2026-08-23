package apitypes

// UserRequestBody is the POST /api/certs/user request body. LocalUsername
// and LocalHostname are the requesting client's own OS identity — for a
// user cert there is no way to request one except via the local client, so
// this is who/where the request actually came from, not optional extra
// context (see docs/dev/changes-next.md).
type UserRequestBody struct {
	PublicKey        string           `json:"public_key" binding:"required"`
	LocalUsername    string           `json:"local_username,omitempty"`
	LocalHostname    string           `json:"local_hostname,omitempty"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// HostSignRequestBody is the POST /api/certs/host/sign request body.
type HostSignRequestBody struct {
	PublicKey        string           `json:"public_key" binding:"required"`
	Hostname         string           `json:"hostname" binding:"required"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// ServiceEnrollRequestBody is the POST /api/certs/service/enroll request
// body. PublicKey may be operator-supplied (BYO key, possibly HSM/PKCS#11/
// encrypted file — the server never sees the private half) or
// client-generated (see docs/dev/ssoossh-context.md, "Service enrollment").
type ServiceEnrollRequestBody struct {
	PublicKey        string           `json:"public_key" binding:"required"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// PAMRequestBody is the POST /api/certs/pam request body. Username is the
// local account pam_ssoossh is authenticating (e.g. who is running `sudo`)
// — the certificate's principal is this, not whatever the browser identity
// that later approves the request happens to be called (see
// docs/features.md, PAM).
type PAMRequestBody struct {
	PublicKey        string           `json:"public_key" binding:"required"`
	Username         string           `json:"username" binding:"required"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// CreateRequestResponse is what every create-request endpoint
// (UserRequestBody/HostSignRequestBody/ServiceEnrollRequestBody/
// PAMRequestBody's handlers)
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
}

// ApproveResponse is POST /api/certs/requests/:id/approve's response body.
// It does not carry the certificate — approval only queues a signing job
// (see docs/signing-pipeline.md); the certificate itself is delivered
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
