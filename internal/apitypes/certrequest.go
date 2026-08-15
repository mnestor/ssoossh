package apitypes

// UserRequestBody is the POST /api/certs/user request body.
type UserRequestBody struct {
	PublicKey        string           `json:"public_key" binding:"required"`
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
// client-generated (see docs/ssoossh-context.md, "Service enrollment").
type ServiceEnrollRequestBody struct {
	PublicKey        string           `json:"public_key" binding:"required"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// CreateRequestResponse is what every create-request endpoint
// (UserRequestBody/HostSignRequestBody/ServiceEnrollRequestBody's handlers)
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
