// Package certmsg holds the message shapes and topic names exchanged over
// the certificate pipeline's queues: the sign queue (Approve → signer) and
// the signed-reply topic (signer → listener/resolver). See
// docs/signing-pipeline.md.
//
// It deliberately depends on nothing but the standard library and
// server/model's type constants — no gorm, no crypto, no config. That's
// what lets server/signer consume these messages without importing
// server/service (which pulls in gorm, and would become an import cycle
// once the listener/resolver lands in that package) and keeps the signer's
// zero-database boundary real rather than aspirational.
package certmsg

import (
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

const (
	// SignQueueTopic is the shared work-queue topic CertRequestService.Approve
	// publishes signing jobs to and the signer consumes from. Shared, not
	// per-request — unlike WaitTopic — since any available signer should be
	// able to pick up any pending job.
	SignQueueTopic = "certrequest.sign"

	// SignedTopic is the shared topic the signer publishes its results to
	// and the listener/resolver consumes from. Shared rather than
	// per-request because only the listener/resolver ever subscribes: there
	// is no fan-out to scope.
	SignedTopic = "certrequest.signed"
)

// WaitTopic returns the per-request wake topic CertRequestService.Wait
// subscribes to and notifyWaiter publishes to. One topic per request, not
// shared — see docs/signing-pipeline.md.
func WaitTopic(requestID string) string {
	return "certrequest.wait." + requestID
}

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

// SigningJob is the sign-queue message Approve publishes. Fully
// self-contained by design: the signer has no database access at all, so
// everything needed to produce the certificate — already resolved against
// server config and the approving identity — must be here. The signer must
// never re-derive or re-check policy; it signs exactly what this says.
type SigningJob struct {
	RequestID        string                `json:"request_id"`
	Type             model.CertificateType `json:"type"`
	PublicKey        string                `json:"public_key"`
	Hostname         string                `json:"hostname,omitempty"`
	Principals       []string              `json:"principals"`
	KeyID            string                `json:"key_id"`
	RequestedOptions RequestedOptions      `json:"requested_options"`
	ValidAfter       time.Time             `json:"valid_after"`
	ValidBefore      time.Time             `json:"valid_before"`
}

// Signing failure codes carried on SignedReply.ErrorCode. These classify
// the failure for logs and metrics; the human-readable detail is in
// SignedReply.Error.
const (
	// ErrCodeUnsupportedType means the signer doesn't handle this
	// certificate type yet (see docs/signing-pipeline.md —
	// user certificates only for now).
	ErrCodeUnsupportedType = "unsupported_type"
	// ErrCodeBadPublicKey means the job's PublicKey didn't parse.
	ErrCodeBadPublicKey = "bad_public_key"
	// ErrCodeCAUnavailable means the CA signing key couldn't be obtained.
	ErrCodeCAUnavailable = "ca_unavailable"
	// ErrCodeSignFailed means the signing operation itself failed.
	ErrCodeSignFailed = "sign_failed"
)

// SignedReply is the signer's result, consumed by the listener/resolver.
// Certificate and Error are mutually exclusive: a non-empty ErrorCode means
// signing failed and no certificate was produced.
//
// The success fields deliberately describe what was *actually signed*
// rather than what was requested, so the listener/resolver can write the
// audit row (model.Certificate) straight from this message instead of
// re-reading and re-interpreting the original request.
type SignedReply struct {
	RequestID string                `json:"request_id"`
	Type      model.CertificateType `json:"type"`

	// Certificate is the signed certificate in authorized_keys format.
	Certificate          string            `json:"certificate,omitempty"`
	Serial               uint64            `json:"serial,omitempty"`
	KeyID                string            `json:"key_id,omitempty"`
	Principals           []string          `json:"principals,omitempty"`
	Hostname             string            `json:"hostname,omitempty"`
	PublicKeyFingerprint string            `json:"public_key_fingerprint,omitempty"`
	CriticalOptions      map[string]string `json:"critical_options,omitempty"`
	Extensions           []string          `json:"extensions,omitempty"`
	ValidAfter           time.Time         `json:"valid_after,omitempty"`
	ValidBefore          time.Time         `json:"valid_before,omitempty"`

	// Error is the human-readable failure detail; ErrorCode is one of the
	// ErrCode* constants above. Both empty on success.
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// Failed reports whether this reply represents a signing failure.
func (r SignedReply) Failed() bool { return r.ErrorCode != "" }
