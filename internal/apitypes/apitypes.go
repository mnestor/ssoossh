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
	// Status is the resolved request status: "approved", "denied", or
	// "expired" (see server/model.CertificateRequestStatus — mirrored here
	// as a plain string rather than importing server/). On the events
	// endpoint this is carried by the SSE event name, not the JSON payload
	// — excluded from JSON encoding/decoding so a stray "status" field
	// can't be confused with it.
	Status string `json:"-"`

	// Certificate is the signed certificate in authorized_keys format,
	// set only when Status is "approved".
	Certificate string `json:"certificate,omitempty"`
}

// CAResponse is GET /api/ca's response body.
type CAResponse struct {
	CA string `json:"ca"`
}
