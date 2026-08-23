package config

import (
	"fmt"
	"time"
)

// SignerConfig is everything the signer needs to run: the broker that
// carries signing jobs and the CA private key that signs them. It is its
// own struct so `ssoosshd sign` has a named, self-contained configuration
// surface — and it is embedded (squashed) in Config so the full server
// shares the exact same fields rather than a parallel copy. Squashing
// keeps the YAML keys top-level (`ssh_key:`, `pubsub:`), so existing
// config files are untouched by the split.
type SignerConfig struct {
	// SSHKey is the SSH CA private key used to sign issued certificates.
	// Inline PEM, not a file path.
	SSHKey string `mapstructure:"ssh_key"`

	// PubSub configures the message broker (gochannel in-process, or NATS
	// for multi-instance and split-process deployments).
	PubSub PubSubConfig `mapstructure:"pubsub"`

	// MaxCertLifetime is the maximum lifetime for user/service/PAM
	// certificates, enforced as a defense-in-depth check before signing.
	// Default 2160h (90 days). Must be > 0 (fail-closed).
	MaxCertLifetime time.Duration `mapstructure:"max_cert_lifetime,string"`

	// MaxHostCertLifetime is the maximum lifetime for host certificates,
	// enforced as a defense-in-depth check before signing. Default 17544h
	// (2 years). Must be > 0 (fail-closed).
	MaxHostCertLifetime time.Duration `mapstructure:"max_host_cert_lifetime,string"`
}

// Validate rejects a signer configuration that cannot issue certificates.
// Called from NewConfig for every mode. For API-only mode, ssh_key is not
// required (keys come from the registry). For full and signer-only modes,
// the key requirement is checked in initSignerHandler when it actually
// tries to load the key. See keysource.NewConfigKeySource for the check.
func (s *SignerConfig) Validate() error {
	if err := s.PubSub.Validate(); err != nil {
		return err
	}
	if s.MaxCertLifetime <= 0 {
		return fmt.Errorf("max_cert_lifetime must be > 0, got %v", s.MaxCertLifetime)
	}
	if s.MaxHostCertLifetime <= 0 {
		return fmt.Errorf("max_host_cert_lifetime must be > 0, got %v", s.MaxHostCertLifetime)
	}
	return nil
}
