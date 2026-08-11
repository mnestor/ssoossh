// Package apitypes is the ssoosshd HTTP API's wire contract: the request
// and response shapes exchanged over /api, shared by both sides so they
// can't drift apart. internal/api (the client, used by client/ and
// pam_ssoossh/) and server/controller both import this package rather than
// each declaring their own copy of the same JSON shapes.
//
// This package holds only plain data types (no HTTP client, no gin
// dependency) so either side can import it without pulling in the other's
// dependencies.
package apitypes

// Terminal certificate-request statuses. These are the complete set of SSE
// event names ssoosshd's events endpoint can send (see
// server/controller/certrequests.go's eventsHandler, which uses the status
// as the event name) — a client must treat every one of them as terminal
// or it will block forever waiting for an event that never comes.
//
// They mirror server/model.CertificateRequestStatus, which is the source of
// truth for the database. They're duplicated here rather than imported
// because client/ and pam_ssoossh/ cannot import server/ (see root
// CLAUDE.md); keeping them in this shared wire-contract package is what
// stops the two sides from drifting apart as new statuses are added.
const (
	StatusApproved = "approved"
	StatusDenied   = "denied"
	StatusExpired  = "expired"
	// StatusEnrolled resolves a service-enrollment request: the payload
	// carries CertificateResult.Code (an enrollment token), not a
	// certificate. See docs/ssoossh-context.md, "Service enrollment".
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

// RequestedOptions are the certificate options a caller may request.
// Server config is always the outer bound on what's actually granted (see
// root CLAUDE.md Hard Constraints) — the server narrows or rejects
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
	// (CertificateTypeService — see docs/ssoossh-context.md, "Service
	// enrollment"). `service retrieve` presents this later to redeem the
	// actual certificate.
	Code string `json:"code,omitempty"`
}

// CAResponse is GET /api/ca's response body.
type CAResponse struct {
	CA string `json:"ca"`
}
