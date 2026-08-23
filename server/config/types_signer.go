package config

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
	return nil
}
