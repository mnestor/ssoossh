package config

import "time"

// CertificateOptions groups the certificate-issuance options for each
// SSH certificate type ssoosshd can sign.
type CertificateOptions struct {
	User    CertOptionsUser    `mapstructure:"user"`
	Service CertOptionsService `mapstructure:"service"`
	Host    CertOptions        `mapstructure:"host"`

	// RequestTTL is how long a pending certificate request (user, host, or
	// service) stays valid for approval before it's treated as expired.
	// Shared across all three types — it's "how stale can an unapproved
	// request get," not a per-type concept like ValidDuration (the issued
	// certificate's own lifetime).
	RequestTTL time.Duration `mapstructure:"request_ttl,string"`

	// SigningTimeout is how long an approved request may sit awaiting
	// signature before the sweep treats it as stranded and fails it (see
	// docs/signing-pipeline.md). A healthy signature
	// takes milliseconds; this only needs to be generous enough for a slow
	// signing backend (an HSM, say) under load.
	//
	// Note this is not measured from approval — nothing records when a
	// request entered signing — but from creation, offset by RequestTTL, so
	// the sweep can never cancel a request that might still be in flight.
	// See the sweep's doc comment for the arithmetic.
	SigningTimeout time.Duration `mapstructure:"signing_timeout,string"`
}

// CertOptions configures issuance of host certificates: the OIDC group
// required to request one, and how long they're valid for.
type CertOptions struct {
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`

	// KeyIDTemplate is a Go text/template string executed against the
	// issuance context to produce the certificate's key ID (see
	// docs/certificate-keyid-template.md for available fields and the
	// per-type fallback rule). Empty falls back to
	// CertificateOptions.User.KeyIDTemplate.
	KeyIDTemplate string `mapstructure:"key_id_template"`
}

// CertOptionsUser configures issuance of user certificates: who may approve
// one, how long they're valid for, and which SSH certificate extensions to
// grant.
type CertOptionsUser struct {
	// RequireGroup is the OIDC group an approver must belong to. Empty — the
	// default — means any authenticated user may approve, which is the
	// behavior every deployment has had so far.
	//
	// Worth setting even though approval is already bound to the requester
	// (see CertRequestService.Approve): the binding answers "is this your
	// request", this answers "are you allowed certificates at all".
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`

	// KeyIDTemplate is the fallback for CertOptionsService.KeyIDTemplate
	// and CertOptions.KeyIDTemplate (host) when either is empty, since
	// user certificates are the common case. See
	// docs/certificate-keyid-template.md.
	KeyIDTemplate string `mapstructure:"key_id_template"`
}

// CertOptionsService configures issuance of service certificates: the OIDC
// group required to request one, how long they're valid for, and which SSH
// certificate extensions to grant.
type CertOptionsService struct {
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`

	// KeyIDTemplate; see CertOptions.KeyIDTemplate and
	// docs/certificate-keyid-template.md. Empty falls back to
	// CertificateOptions.User.KeyIDTemplate.
	KeyIDTemplate string `mapstructure:"key_id_template"`
}
