// Package api is the ssoosshd HTTP API client shared by client and
// pam_ssoossh (neither may import the other or server/ directly — see root
// CLAUDE.md's hard constraint on cross-package imports; this is the
// internal/ home for what they share). Built on resty.dev/v3.
package api

// RequestedOptions are the certificate options a caller may request.
// Server config is always the outer bound on what's actually granted (see
// root CLAUDE.md Hard Constraints) — the server narrows or rejects
// anything it doesn't permit, it never grants more than was requested.
//
// Deliberately independent of server/service.RequestedOptions: this is the
// wire contract, free to evolve on its own rather than being coupled to
// the server's internal type, and server/ can't be imported from here
// anyway.
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

// CertificateResult is the outcome of a certificate request once resolved.
type CertificateResult struct {
	// Status is the resolved request status: "approved", "denied", or
	// "expired" (see server/model.CertificateRequestStatus — mirrored here
	// as a plain string rather than importing server/).
	Status string

	// Certificate is the signed certificate in authorized_keys format,
	// set only when Status is "approved".
	Certificate string
}
