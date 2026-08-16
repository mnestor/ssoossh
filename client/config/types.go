package config

type Config struct {
	Server        string `mapstructure:"server"`
	CAPubkey      string `mapstructure:"capubkey"`
	SkipVerifySSL bool   `mapstructure:"insecure_skip_verify"`

	SSHKey SSHKeyOptions `mapstructure:"sshkey"`

	// this is the default and recommended way to use ssoossh
	UseAgent bool `mapstructure:"use_agent"`

	// use files if agent is not available
	FallbackFileAgent bool   `mapstructure:"fallback_file_agent"`
	Filename          string `mapstructure:"key_filename"`

	// Username is reserved and currently read by nothing. It was meant to
	// name a service account, but that is chosen by the approver in the web
	// UI: enrollment requests are unauthenticated, so the client has no
	// session against which to validate one. The local ambiguity it was
	// meant to settle — a keypair created under the service's own account
	// versus under a person's — is about where the key file lives, which
	// Filename already answers, not about the certificate's principal. See
	// docs/delivery-phase8-service.md.
	Username string `mapstructure:"username"`

	TryOpenBrowser bool `mapstructure:"try_open_browser"`

	// FIPS steers key generation toward algorithms accepted by SSH
	// implementations running in FIPS mode. It is advisory: it changes the
	// default key type and warns about non-approved choices, but never
	// refuses one — the operator knows their environment better than this
	// does. See FIPSEnabled for how it resolves, and ResolveSSHKey for what
	// it affects.
	//
	// A pointer so "unset" is distinguishable from "explicitly false":
	// unset falls back to whether the Go runtime is itself in FIPS 140-3
	// mode.
	FIPS *bool `mapstructure:"fips"`

	// FIPSEnforced is true when `fips: true` was set specifically by the
	// system enforce file (LockedFile below), as opposed to a user or local
	// config file. It is computed by NewConfig, not read from any file
	// directly — see the mapstructure tag. ResolveSSHKey treats it as the
	// difference between advisory (warn) and hard (error) FIPS steering.
	FIPSEnforced bool `mapstructure:"-"`

	// This is only settable in /etc/ssoossh/ssoossh.yaml
	// if a setting is in this file then clients CANNOT change it
	LockedFile string `mapstructure:"enforce"`
}

// SSHKeyOptions selects the algorithm and size for the keypair the client
// generates. Both are optional; see ResolveSSHKey for the defaults and the
// rules applied to them.
type SSHKeyOptions struct {
	// Type is the key algorithm: see the SSHKeyType constants. Empty picks
	// a default appropriate to whether FIPS mode is in effect.
	Type SSHKeyType `mapstructure:"type"`

	// Size means different things per algorithm, and nothing at all for
	// ed25519 (which has exactly one size):
	//
	//   - ecdsa: the NIST curve — 256, 384, or 521
	//   - rsa:   the modulus size in bits, at least 2048
	//
	// Zero takes the default for the chosen algorithm.
	Size int `mapstructure:"size"`
}

// SSHKeyType names a key algorithm as written in configuration, e.g.
// `type: ed25519`.
type SSHKeyType string

const (
	SSHKeyTypeEd25519 SSHKeyType = "ed25519"
	SSHKeyTypeECDSA   SSHKeyType = "ecdsa"
	SSHKeyTypeRSA     SSHKeyType = "rsa"
)
