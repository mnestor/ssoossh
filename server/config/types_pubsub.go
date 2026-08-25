package config

import (
	"fmt"
	"os"
)

// PubSubBackend identifies which message broker backend to use.
const (
	PubSubBackendGoChannel = "gochannel"
	PubSubBackendNATS      = "nats"
)

// PubSubConfig configures the message broker behind the certificate
// pipeline. It carries signing requests, approvals, and replies, and selects
// the backend that does so.
//
// gochannel (in-process) is the default and suitable for single-instance
// deployments. NATS is required for multi-instance deployments where
// multiple ssoosshd processes share a database.
type PubSubConfig struct {
	// Backend selects which message-broker implementation to use.
	// "gochannel" (default) is in-process, suitable for single-instance.
	// "nats" is required for multi-instance deployments.
	Backend string `mapstructure:"backend"`

	// NATS holds NATS-specific configuration. Required when Backend is "nats".
	NATS NATSConfig `mapstructure:"nats"`
}

// NATSConfig configures the NATS broker connection: the server URL and
// mTLS client certificate, key, and CA material. All three certificate
// files must be present and readable when Backend is "nats"; this is
// validated at startup.
type NATSConfig struct {
	// URL is the NATS server connection string, e.g. "nats://nats.example.com:4222".
	// Empty disables NATS (leaves Backend at gochannel).
	URL string `mapstructure:"url"`

	// CertFile is the path to the client certificate for mTLS authentication.
	// Required when URL is set.
	CertFile string `mapstructure:"cert_file"`

	// KeyFile is the path to the client certificate's private key.
	// Required when URL is set.
	KeyFile string `mapstructure:"key_file"`

	// CAFile is the path to the CA certificate to verify the NATS server.
	// Required when URL is set.
	CAFile string `mapstructure:"ca_file"`
}

// Validate ensures the PubSub configuration is usable: if the NATS backend
// is selected, all three certificate files must be specified and readable.
// Called at startup so a bad value stops the process rather than failing
// on first publication.
func (c *PubSubConfig) Validate() error {
	if c.Backend == "" {
		c.Backend = PubSubBackendGoChannel
	}

	if c.Backend != PubSubBackendNATS {
		return nil
	}

	if c.NATS.URL == "" {
		return fmt.Errorf("pubsub.backend is 'nats' but pubsub.nats.url is not set")
	}

	// All three certificate files are required for mTLS.
	for name, path := range map[string]string{
		"pubsub.nats.cert_file": c.NATS.CertFile,
		"pubsub.nats.key_file":  c.NATS.KeyFile,
		"pubsub.nats.ca_file":   c.NATS.CAFile,
	} {
		if path == "" {
			return fmt.Errorf("%s is required when using the NATS backend", name)
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("%s %q: %w", name, path, err)
		}
	}

	return nil
}
