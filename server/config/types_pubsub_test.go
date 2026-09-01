package config

// Test methodology: table-driven unit tests over PubSubConfig.Validate.
// The NATS backend is validated at startup on purpose — a missing mTLS
// file must stop the process, not fail on the first publication — so each
// case here is a startup misconfiguration and the message it must carry.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// natsFiles writes the three mTLS files a valid NATS backend needs and
// returns their paths, so each case below can drop exactly one thing.
func natsFiles(t *testing.T) (cert, key, ca string) {
	t.Helper()

	dir := t.TempDir()
	cert = filepath.Join(dir, "client.crt")
	key = filepath.Join(dir, "client.key")
	ca = filepath.Join(dir, "ca.crt")
	for _, p := range []string{cert, key, ca} {
		if err := os.WriteFile(p, []byte("pem"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
	}
	return cert, key, ca
}

func TestPubSubConfig_Validate(t *testing.T) {
	t.Parallel()

	cert, key, ca := natsFiles(t)

	tests := []struct {
		name     string
		cfg      PubSubConfig
		wantText string
	}{
		{
			name: "the gochannel backend needs nothing",
			cfg:  PubSubConfig{Backend: PubSubBackendGoChannel},
		},
		{
			name: "a fully configured nats backend",
			cfg: PubSubConfig{
				Backend: PubSubBackendNATS,
				NATS:    NATSConfig{URL: "nats://broker.test:4222", CertFile: cert, KeyFile: key, CAFile: ca},
			},
		},
		{
			name:     "nats without a url",
			cfg:      PubSubConfig{Backend: PubSubBackendNATS},
			wantText: "pubsub.nats.url",
		},
		{
			name: "nats without a client certificate",
			cfg: PubSubConfig{
				Backend: PubSubBackendNATS,
				NATS:    NATSConfig{URL: "nats://broker.test:4222", KeyFile: key, CAFile: ca},
			},
			wantText: "pubsub.nats.cert_file",
		},
		{
			name: "nats without a client key",
			cfg: PubSubConfig{
				Backend: PubSubBackendNATS,
				NATS:    NATSConfig{URL: "nats://broker.test:4222", CertFile: cert, CAFile: ca},
			},
			wantText: "pubsub.nats.key_file",
		},
		{
			name: "nats without a ca certificate",
			cfg: PubSubConfig{
				Backend: PubSubBackendNATS,
				NATS:    NATSConfig{URL: "nats://broker.test:4222", CertFile: cert, KeyFile: key},
			},
			wantText: "pubsub.nats.ca_file",
		},
		{
			// The path is checked readable at startup so a typo fails the
			// process here rather than the first signing request later.
			name: "nats with a certificate file that does not exist",
			cfg: PubSubConfig{
				Backend: PubSubBackendNATS,
				NATS:    NATSConfig{URL: "nats://broker.test:4222", CertFile: filepath.Join(t.TempDir(), "missing.crt"), KeyFile: key, CAFile: ca},
			},
			wantText: "missing.crt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if tt.wantText == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() error = nil, want one mentioning %q", tt.wantText)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("Validate() error = %q, want it to mention %q", err, tt.wantText)
			}
		})
	}
}

// An empty backend is normalised to gochannel in place, so later wiring can
// switch on the constant without re-deriving the default.
func TestPubSubConfig_ShouldDefaultEmptyBackendToGoChannel(t *testing.T) {
	t.Parallel()

	c := PubSubConfig{}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	if c.Backend != PubSubBackendGoChannel {
		t.Errorf("Backend after Validate() = %q, want %q", c.Backend, PubSubBackendGoChannel)
	}
}
