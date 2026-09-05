// Package apitypes is the ssoosshd HTTP API's wire contract: the request
// and response shapes exchanged over /api, shared by both sides so they
// can't drift apart. internal/api (the client, used by client/) and
// server/controller both import this package rather than each declaring
// their own copy of the same JSON shapes.
//
// This package holds only plain data types (no HTTP client, no gin
// dependency) so either side can import it without pulling in the other's
// dependencies.
//
// pam_ssoossh (github.com/mnestor/ssoossh-pam) speaks this same contract in
// C. It shares no code with this package and never will, so nothing at
// compile time connects the two: a renamed tag here is a silent break there.
// The goldens in testdata/ are what the other side tests against, and
// docs/wire-contract.json versions them — change a shape and both have to
// move, in this order:
//
//	go test ./internal/apitypes/ -update
//	make openapi
//	make wire-contract
//
// then land the matching change in ssoossh-pam before releasing either side.
// See docs/internals/wire-types.md.
package apitypes

import "time"

// Terminal certificate-request statuses. These are the complete set of SSE
// event names ssoosshd's events endpoint can send (see
// server/controller/certrequests.go's eventsHandler, which uses the status
// as the event name) — a client must treat every one of them as terminal
// or it will block forever waiting for an event that never comes.
//
// They mirror server/model.CertificateRequestStatus, which is the source of
// truth for the database. They're duplicated here rather than imported
// because client/ cannot import server/ (see
// docs/internals/invariants.md); keeping them in this shared wire-contract
// package is what stops the two sides drifting apart as statuses are added.
const (
	StatusApproved = "approved"
	StatusDenied   = "denied"
	StatusExpired  = "expired"
	// StatusEnrolled resolves a service-enrollment request: the payload
	// carries CertificateResult.Code (an enrollment token), not a
	// certificate. See docs/internals/design-brief.md, "Service enrollment".
	StatusEnrolled = "enrolled"
	// StatusFailed means the request was approved but signing failed, or a
	// sweep invalidated it. No certificate will ever arrive for it.
	StatusFailed = "failed"
)

// TerminalStatuses lists every status that resolves a certificate request.
// Clients should register for all of them.
func TerminalStatuses() []string {
	return []string{StatusApproved, StatusDenied, StatusExpired, StatusEnrolled, StatusFailed}
}

// Error codes carried on Envelope.ErrorCode when a request fails. These are
// the machine-readable classification a client branches on; the human-readable
// message is in Envelope.Error. Adding a code here is a breaking change to
// callers that do not yet handle it — bump the API version and leave these
// stable once defined.
const (
	// ErrorCodeInvalidRequest means the request body was malformed or a
	// validation check failed (400 Bad Request).
	ErrorCodeInvalidRequest = "invalid_request"
	// ErrorCodeUnauthenticated means the request lacked a valid session
	// (401 Unauthorized).
	ErrorCodeUnauthenticated = "unauthenticated"
	// ErrorCodeForbidden means the authenticated caller cannot act on this
	// resource (403 Forbidden).
	ErrorCodeForbidden = "forbidden"
	// ErrorCodeNotFound means the requested resource does not exist
	// (404 Not Found).
	ErrorCodeNotFound = "not_found"
	// ErrorCodeUnavailable means the resource once existed but can no longer
	// be delivered (410 Gone).
	ErrorCodeUnavailable = "unavailable"
	// ErrorCodeRateLimited means the caller has exceeded a rate limit
	// (429 Too Many Requests).
	ErrorCodeRateLimited = "rate_limited"
	// ErrorCodeNotImplemented means the endpoint exists but the handler is not
	// yet implemented (501 Not Implemented).
	ErrorCodeNotImplemented = "not_implemented"
	// ErrorCodeInternalError is a catch-all for server-side failures
	// (500 Internal Server Error). Callers should treat this as transient
	// and retry; the server will have logged details.
	ErrorCodeInternalError = "internal_error"
)

// RequestedOptions are the certificate options a caller may request.
// Server config is always the outer bound on what's actually granted (see
// docs/internals/invariants.md) — the server narrows or rejects
// anything it doesn't permit, it never grants more than was requested.
//
// Deliberately independent of server/service.RequestedOptions: this is the
// wire contract, free to evolve on its own rather than being coupled to
// the server's internal representation. server/controller converts between
// the two at the request-binding boundary.
type RequestedOptions struct {
	// Extensions are the SSH certificate extensions requested (e.g.
	// "permit-pty", "permit-agent-forwarding").
	Extensions []string `json:"extensions,omitempty"`

	// ForceCommand is the SSH "force-command" critical option request.
	ForceCommand string `json:"force_command,omitempty"`

	// SourceAddresses are the caller's own interface addresses, unioned
	// server-side with the address the request was observed from.
	SourceAddresses []string `json:"source_addresses,omitempty"`

	// NoTouchRequired requests the OpenSSH "no-touch-required" extension.
	// Only meaningful for service enrollment of a hardware-backed sk- key.
	NoTouchRequired bool `json:"no_touch_required,omitempty"`
}

// CertificateResult is the outcome of a certificate request once resolved
// — the payload of the events endpoint's terminal SSE event
// (server/controller/certrequests.go's eventsHandler) and of the web UI's
// approve response.
type CertificateResult struct {
	// Status is the resolved request status: "approved", "denied",
	// "expired", or "enrolled" (see server/model.CertificateRequestStatus —
	// mirrored here as a plain string rather than importing server/). On
	// the events endpoint this is carried by the SSE event name, not the
	// JSON payload — excluded from JSON encoding/decoding so a stray
	// "status" field can't be confused with it.
	Status string `json:"-"`

	// Certificate is the signed certificate in authorized_keys format,
	// set only when Status is "approved".
	Certificate string `json:"certificate,omitempty"`

	// Code is the enrollment token, set only when Status is "enrolled"
	// (CertificateTypeService — see docs/internals/design-brief.md, "Service
	// enrollment"). `service retrieve` presents this later to redeem the
	// actual certificate.
	Code string `json:"code,omitempty"`

	// ServiceAccount is the account the approver chose, set only alongside
	// Code. It is the sole principal of every certificate the code
	// redeems, and the approval happens in a browser the operator running
	// `service enroll` is not looking at — so without this the CLI can
	// print a code but not say whose identity it carries.
	ServiceAccount string `json:"service_account,omitempty"`

	// ExpiresAt is when Code stops being redeemable, set only alongside it.
	// This bounds the code, not the certificates it produces; those get
	// their own lifetime at each redemption. A pointer so an outcome that
	// has no expiry omits the field rather than sending the zero time.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// CAResponse is GET /api/ca's response body.
type CAResponse struct {
	CA string `json:"ca" validate:"required"`
}

// Envelope is the shape every JSON response from ssoosshd carries:
// exactly one of Data or Error is meaningful.
//
// Uniform across the whole API on purpose. The alternative — bare payloads
// on the endpoints the Go client uses and an envelope on the ones the web
// UI uses — means two decode paths and two things to remember, for no gain.
// See .claude/rules/server-api.md.
//
// The server writes the error half from
// middleware.ErrorHandlerMiddleware, so a handler that fails never has to
// construct one.
type Envelope[T any] struct {
	// Data is the payload. Absent (zero) on an error response.
	Data T `json:"data"`

	// Error is a human-readable message, empty on success. Callers should
	// branch on the HTTP status rather than this string; it is for humans
	// and logs.
	Error string `json:"error,omitempty"`

	// ErrorCode is a machine-readable error classifier, empty on success.
	// One of the ErrorCode* constants when an error occurred; use this to
	// decide whether to retry or branch on the failure mode. It is stable
	// within a major API version.
	ErrorCode string `json:"error_code,omitempty"`
}
