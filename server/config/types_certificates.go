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

// CertOptionsUser configures issuance of user certificates: how long
// they're valid for, and which SSH certificate extensions to grant.
type CertOptionsUser struct {
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
