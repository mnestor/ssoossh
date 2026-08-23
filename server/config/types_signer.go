package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

// HSMConfig configures a PKCS#11 (HSM) backed CA key. When Module is set the
// signer loads the CA key from the token instead of ssh_key. Developed and
// tested against SoftHSM2; any PKCS#11 module should work. Ed25519 CA keys
// are not supported on this path (Go PKCS#11 limitation) — use ECDSA or RSA.
type HSMConfig struct {
	// Module is the absolute path to the PKCS#11 shared library,
	// e.g. /usr/lib/softhsm/libsofthsm2.so. Setting it enables HSM mode.
	Module string `mapstructure:"module"`
	// TokenLabel selects the token (softhsm2-util --init-token --label ...).
	TokenLabel string `mapstructure:"token_label"`
	// PIN is the user PIN. Mutually exclusive with PINFile.
	PIN string `mapstructure:"pin"`
	// PINFile is a path whose trimmed contents are the user PIN. Preferred
	// over inline PIN so the config file can stay world-readable-ish.
	PINFile string `mapstructure:"pin_file"`
	// KeyLabel selects the key pair by CKA_LABEL. At least one of KeyLabel
	// or KeyID is required; when both are set both must match.
	KeyLabel string `mapstructure:"key_label"`
	// KeyID selects the key pair by CKA_ID, hex-encoded (pkcs11-tool --id).
	KeyID string `mapstructure:"key_id"`
}

// Enabled reports whether an HSM-backed CA key is configured.
func (h *HSMConfig) Enabled() bool { return h.Module != "" }

// ResolvePIN returns the user PIN, reading PINFile if configured. Validate
// has already enforced that exactly one of PIN/PINFile is set.
func (h *HSMConfig) ResolvePIN() (string, error) {
	if h.PINFile == "" {
		return h.PIN, nil
	}
	b, err := os.ReadFile(h.PINFile)
	if err != nil {
		return "", fmt.Errorf("read hsm pin_file: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// KeyIDBytes decodes the hex KeyID; empty when unset.
func (h *HSMConfig) KeyIDBytes() ([]byte, error) {
	if h.KeyID == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(h.KeyID)
	if err != nil {
		return nil, fmt.Errorf("hsm key_id is not valid hex: %w", err)
	}
	return b, nil
}

// validate rejects an HSM block that cannot select and unlock a key.
func (h *HSMConfig) validate() error {
	if h.TokenLabel == "" {
		return fmt.Errorf("hsm token_label is required")
	}
	if (h.PIN == "") == (h.PINFile == "") {
		return fmt.Errorf("exactly one of hsm pin or pin_file is required")
	}
	if h.KeyLabel == "" && h.KeyID == "" {
		return fmt.Errorf("at least one of hsm key_label or key_id is required")
	}
	if _, err := h.KeyIDBytes(); err != nil {
		return err
	}
	return nil
}

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

	// HSM optionally sources the CA key from a PKCS#11 token instead of
	// ssh_key. Exactly one of the two may be set at the config level (API
	// mode has neither; signing modes require one to be set).
	HSM HSMConfig `mapstructure:"hsm"`

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
// Called from NewConfig for every mode. For API-only mode, neither ssh_key
// nor hsm is required (keys come from the registry). For full and
// signer-only modes, the key requirement is checked in initSignerHandler
// when it actually tries to load the key. See keysource.NewConfigKeySource
// for the check.
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

	hsmEnabled := s.HSM.Enabled()
	switch {
	case s.SSHKey != "" && hsmEnabled:
		return fmt.Errorf("exactly one of ssh_key and hsm may be set: two CA key sources would be ambiguous")
	case hsmEnabled:
		if err := s.HSM.validate(); err != nil {
			return err
		}
	}
	return nil
}
