package service

// RequestedOptions are the client-supplied certificate options carried on a
// CertificateRequest, narrowed against server config (config.CertOptionsUser
// / CertOptionsService / CertOptions) before anything reaches the web UI or
// gets signed — see root CLAUDE.md Hard Constraints ("server config is the
// outer bound"). Field names and semantics follow
// docs/what-ssoossh-is.md's "Certificate terms" section.
type RequestedOptions struct {
	// Extensions are the SSH certificate extensions requested (e.g.
	// "permit-pty", "permit-agent-forwarding"). Fail-open: sshd ignores
	// any it doesn't recognize.
	Extensions []string `json:"extensions,omitempty"`

	// ForceCommand is the SSH "force-command" critical option: the
	// certificate may only run this command. Fail-closed: sshd rejects
	// the certificate outright if it can't honor a critical option.
	ForceCommand string `json:"force_command,omitempty"`

	// SourceAddresses are the client's own interface addresses, unioned
	// server-side with the address the request was observed from to form
	// the SSH "source-address" critical option (see
	// docs/what-ssoossh-is.md, "Certificate lifetime and context policy" —
	// NAT means neither address alone is sufficient). Unverified client
	// input; server config is the ceiling on what's actually granted.
	SourceAddresses []string `json:"source_addresses,omitempty"`

	// NoTouchRequired requests the OpenSSH "no-touch-required" extension.
	// Only meaningful for service enrollment of a hardware-backed sk- key
	// (see root CLAUDE.md Hard Constraints) — ignored for client-generated
	// keys on every other path.
	NoTouchRequired bool `json:"no_touch_required,omitempty"`
}
