package config

import "github.com/mnestor/ssoossh/internal/fipsmode"

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
	// implementations running in FIPS mode. When it's in effect, a
	// non-approved sshkey.type is a hard error — see ResolveSSHKey. See
	// FIPSEnabled for how it resolves.
	//
	// A pointer so "unset" is distinguishable from "explicitly false":
	// unset falls back to whether the Go runtime is itself in FIPS 140-3
	// mode. Explicit `fips: false` in a user-writable config file is the
	// one escape hatch from enforcement — unless a system enforce file
	// locks `fips: true`, which already wins over it via viper's merge
	// order regardless of FIPSEnforced below.
	FIPS *bool `mapstructure:"fips"`

	// FIPSEnforced is true when `fips: true` was set specifically by the
	// system enforce file (LockedFile below), as opposed to a user or local
	// config file. It is computed by NewConfig, not read from any file
	// directly — see the mapstructure tag. It no longer changes
	// ResolveSSHKey's behavior (a non-approved type is now always a hard
	// error under FIPS, regardless of where `fips: true` came from); it's
	// kept for what it already reports and tests, in case future policy
	// needs to distinguish the setting's origin again.
	FIPSEnforced bool `mapstructure:"-"`

	// This is only settable in /etc/ssoossh/ssoossh.yaml
	// if a setting is in this file then clients CANNOT change it
	LockedFile string `mapstructure:"enforce"`

	// CertificateExtensions controls which SSH certificate extensions are
	// requested. Each boolean is an opt-out from the full interactive set
	// (loginExtensions in ssh_login.go). Flags override config, which
	// overrides these defaults. Policy-forbidden extensions (see
	// ForbiddenCertificateExtensions) are subtracted unconditionally,
	// after flag/config resolution — a flag cannot re-add what policy
	// forbids.
	CertificateExtensions CertificateExtensionOptions `mapstructure:"certificate_extensions"`

	// ForbiddenCertificateExtensions names extensions that policy forbids.
	// This is set only by platform-native policy (GPO registry on Windows,
	// managed preferences on macOS, enforce file on Linux). It is not read
	// from user config, not bound to flags, and not merged — it acts as an
	// unconditional floor that subtracts from any flag/config result.
	ForbiddenCertificateExtensions []string `mapstructure:"-"`

	// Sources records the configuration merge chain in the order it was
	// applied, including locations that were absent and files that failed
	// to parse. Populated by newConfig, never by unmarshalling, and read by
	// `--debug`. It is the only place a silently-skipped bad config file
	// becomes visible — see ConfigSource.
	Sources []ConfigSource `mapstructure:"-"`

	// SetByFlag names the settings a command-line flag actually set, keyed
	// by the viper key the flag binds to (e.g.
	// "certificate_extensions.no_pty"). Populated by newConfig from
	// bindFlags, never unmarshalled.
	//
	// It exists because binding is lossy in the direction that matters for
	// telling a user where a value came from: a flag and a config file both
	// end up as the same field on this struct, so a message about a setting
	// cannot otherwise name the layer the reader is able to change. Blaming
	// the config file for something someone just typed on the command line
	// sends them looking in the wrong place.
	//
	// Only consulted for user-facing attribution. Precedence is viper's
	// business and is not affected by this.
	SetByFlag map[string]bool `mapstructure:"-"`
}

// SSHKeyOptions selects the algorithm and size for the keypair the client
// generates. Both are optional; see ResolveSSHKey for the defaults and the
// rules applied to them.
type SSHKeyOptions struct {
	// Type is the key algorithm: see the SSHKeyType constants. Empty picks
	// the default (ECDSA P-384) unconditionally.
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
// `type: ed25519`. It aliases fipsmode.SSHKeyType: the type and its
// approval/default policy live in internal/fipsmode since the server needs
// them too, but client code keeps referring to them under the config
// package's own name.
type SSHKeyType = fipsmode.SSHKeyType

const (
	SSHKeyTypeEd25519 = fipsmode.SSHKeyTypeEd25519
	SSHKeyTypeECDSA   = fipsmode.SSHKeyTypeECDSA
	SSHKeyTypeRSA     = fipsmode.SSHKeyTypeRSA
)

// CertificateExtensionOptions controls which SSH certificate extensions
// are requested. Each field is an opt-out from the full interactive set.
// Unset (false) means "request this extension", true means "do not request it".
type CertificateExtensionOptions struct {
	NoPTY             bool `mapstructure:"no_pty"`
	NoAgentForwarding bool `mapstructure:"no_agent_forwarding"`
	NoPortForwarding  bool `mapstructure:"no_port_forwarding"`
	NoX11Forwarding   bool `mapstructure:"no_x11_forwarding"`
	NoUserRC          bool `mapstructure:"no_user_rc"`
}
