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
	Module string `mapstructure:"module" example:"\"/usr/lib/softhsm/libsofthsm2.so\""`
	// TokenLabel selects the token (softhsm2-util --init-token --label ...).
	TokenLabel string `mapstructure:"token_label" example:"\"ssoossh-ca\""`
	// PIN is the user PIN. Mutually exclusive with PINFile.
	PIN string `mapstructure:"pin" secret:"true" example:"\"1234\""`
	// PINFile is a path whose trimmed contents are the user PIN. Preferred
	// over inline PIN so the config file can stay world-readable-ish.
	PINFile string `mapstructure:"pin_file" example:"\"/etc/ssoossh/hsm-pin\""`
	// KeyLabel selects the key pair by CKA_LABEL. At least one of KeyLabel
	// or KeyID is required; when both are set both must match.
	KeyLabel string `mapstructure:"key_label" example:"\"ssoossh-ca\""`
	// KeyID selects the key pair by CKA_ID, hex-encoded (pkcs11-tool --id).
	KeyID string `mapstructure:"key_id" example:"\"01\""`
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

// AgentConfig configures an ssh-agent as the CA key source. The agent holds
// the private key; ssoosshd sends it blobs to sign and never sees the key.
//
// This is the only source that reaches an HSM without linking PKCS#11 into
// ssoosshd: `ssh-add -s /path/to/module` loads a token into the agent, and
// the signer then talks the agent protocol to a key that never left the
// hardware. It is also the only source that can hold an Ed25519 CA key
// outside the config file, since the PKCS#11 path cannot sign with one.
//
// See https://mnestor.github.io/ssoossh/operations/ssh-agent/.
type AgentConfig struct {
	// Socket is the agent's Unix socket path. Empty falls back to
	// SSH_AUTH_SOCK, which is convenient interactively and worth setting
	// explicitly for a service, where inheriting a socket from whatever
	// environment systemd happened to pass is not a decision anyone made.
	Socket string `mapstructure:"socket" example:"\"/run/ssoossh/agent.sock\""`

	// KeyFingerprint selects the CA key by SHA256 fingerprint, the form
	// `ssh-add -l` prints. Optional when the agent holds exactly one key;
	// required when it holds more, because "the first key" depends on the
	// order they were added and is not an identity.
	KeyFingerprint string `mapstructure:"key_fingerprint" example:"\"SHA256:WdJsHl3uxM2S7QwPQKBXaHAW8bKNToZMbzrcGq4DZvA\""`
}

// Enabled reports whether an agent-backed CA key is configured.
func (a *AgentConfig) Enabled() bool { return a.Socket != "" || a.KeyFingerprint != "" }

// ResolvedSocket returns the agent socket path, falling back to
// SSH_AUTH_SOCK when socket is unset.
func (a *AgentConfig) ResolvedSocket() string {
	if a.Socket != "" {
		return a.Socket
	}
	return os.Getenv("SSH_AUTH_SOCK")
}

// validate rejects an agent block that cannot reach an agent.
func (a *AgentConfig) validate() error {
	if a.ResolvedSocket() == "" {
		return fmt.Errorf("ssh_key_agent.socket is required when SSH_AUTH_SOCK is not set")
	}
	if a.KeyFingerprint != "" && !strings.HasPrefix(a.KeyFingerprint, "SHA256:") {
		return fmt.Errorf("ssh_key_agent.key_fingerprint %q is not a SHA256 fingerprint: use the form ssh-add -l prints", a.KeyFingerprint)
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
	// Inline PEM, not a file path. Exactly one of ssh_key, ssh_key_file or
	// hsm may be set, and one of them must be: startup fails without a CA
	// key.
	//
	//	ssh_key: |
	//	  -----BEGIN OPENSSH PRIVATE KEY-----
	//	  -----END OPENSSH PRIVATE KEY-----
	SSHKey string `mapstructure:"ssh_key" secret:"true"`

	// SSHKeyFile reads the CA private key from a file instead of holding it
	// inline, so the key can be a mounted secret, a systemd credential or a
	// file with its own ownership and mode, rather than config text. It is
	// the middle option between an inline key and a PKCS#11 token: the key
	// is still a file on disk that the process reads, but it is no longer
	// part of the config file, so the config can be world-readable, checked
	// into configuration management, or rendered by a template without
	// carrying the CA with it.
	//
	// Read once at startup, so an unreadable or malformed key is a boot
	// failure rather than a signing failure later. The contents are used
	// verbatim: unlike a PIN or a password, PEM is whitespace-sensitive at
	// its delimiters and nothing is trimmed.
	//
	//	ssh_key_file: /etc/ssoossh/ca-key
	SSHKeyFile string `mapstructure:"ssh_key_file" example:"\"/etc/ssoossh/ca-key\""`

	// SSHKeyPassphrase decrypts a passphrase-protected CA key, from either
	// ssh_key or ssh_key_file. Prefer ssh_key_passphrase_file: this value is
	// a secret sitting in a config file.
	//
	// Understand what it protects before relying on it. The passphrase
	// guards the key AT REST and nothing else: once parsed, the private key
	// is plaintext in the signer's memory exactly as an unencrypted one
	// would be. It defends against the key file leaking on its own -- a
	// backup, a volume snapshot, a stray copy in a support bundle -- and it
	// defends against that only if the passphrase is not stored beside the
	// key. A passphrase in the same config file as ssh_key, or in a file
	// mounted next to ssh_key_file with the same ownership, buys nothing:
	// whoever reads one reads the other.
	SSHKeyPassphrase string `mapstructure:"ssh_key_passphrase" secret:"true" example:"\"\""`

	// SSHKeyPassphraseFile reads the passphrase from a file instead, so it
	// can come from a mounted secret or a systemd credential with different
	// ownership than the key itself -- which is the arrangement that makes
	// a passphrase worth having at all. Exactly one of ssh_key_passphrase
	// or ssh_key_passphrase_file may be set.
	//
	// Read once at startup. Trailing whitespace is trimmed, since an
	// editor's newline is never part of a passphrase.
	SSHKeyPassphraseFile string `mapstructure:"ssh_key_passphrase_file" example:"\"/run/secrets/ssoossh-ca-passphrase\""`

	// HSM optionally sources the CA key from a PKCS#11 token instead of
	// ssh_key. Exactly one of the two may be set at the config level (API
	// mode has neither; signing modes require one to be set). See
	// https://mnestor.github.io/ssoossh/operations/hsm/ for setup.
	//
	// Supported: ECDSA P-256/384/521, RSA >= 2048. Ed25519 is not supported
	// by PKCS#11 here; keep ssh_key for an Ed25519 CA.
	HSM HSMConfig `mapstructure:"hsm"`

	// Agent optionally sources the CA key from a running ssh-agent instead
	// of ssh_key, ssh_key_file or hsm. Exactly one of the four may be set.
	//
	// Unlike hsm, this needs no PKCS#11 module in this process -- an agent
	// with a token loaded (`ssh-add -s`) gives HSM-backed signing from a
	// cgo-free build. Unlike hsm, it also supports Ed25519.
	Agent AgentConfig `mapstructure:"ssh_key_agent"`

	// PubSub configures the message broker behind the certificate pipeline.
	// gochannel is in-process; NATS is required for multi-instance and
	// split-process deployments.
	PubSub PubSubConfig `mapstructure:"pubsub"`

	// MaxCertLifetime is the maximum lifetime for user/service/PAM
	// certificates, enforced as a defense-in-depth check before signing.
	// Must be greater than zero; a non-positive value fails startup.
	MaxCertLifetime time.Duration `mapstructure:"max_cert_lifetime,string" default:"2160h"`

	// MaxServiceCertLifetime is the maximum lifetime for service
	// certificates, enforced as a defense-in-depth check before signing.
	// Service enrollments default to 8760h (cert_options.service
	// valid_duration), so this cap carries headroom over its default.
	// Must be greater than zero; a non-positive value fails startup.
	MaxServiceCertLifetime time.Duration `mapstructure:"max_service_cert_lifetime,string" default:"17544h"`

	// resolvedSSHKey is the CA private key PEM after SSHKeyFile has been
	// read, populated by Validate. Unexported so it cannot arrive from YAML
	// and cannot be serialized into the auditor-visible effective config --
	// the same reasoning as SMTPConfig.resolvedPassword.
	resolvedSSHKey string

	// resolvedSSHKeyPassphrase is the passphrase after
	// SSHKeyPassphraseFile has been read. Unexported for the same reason.
	resolvedSSHKeyPassphrase string
}

// ResolvedSSHKey returns the CA private key PEM after ssh_key_file
// resolution, or the empty string when the key comes from an HSM or is not
// configured at all.
//
// It falls back to the inline SSHKey when Validate has not run, so a
// SignerConfig built as a struct literal -- which tests and any future
// programmatic construction do -- still yields its key. The fallback is
// exact rather than lenient: resolvedSSHKey differs from SSHKey only when
// SSHKeyFile is set, and resolveCAKey rejects both being set at once. A
// literal that sets SSHKeyFile without validating gets the empty string and
// fails loudly at NewConfigKeySource, which is the correct outcome for a
// file nobody read.
func (s *SignerConfig) ResolvedSSHKey() string {
	if s.resolvedSSHKey != "" {
		return s.resolvedSSHKey
	}
	return s.SSHKey
}

// ResolvedSSHKeyPassphrase returns the CA key passphrase after
// ssh_key_passphrase_file resolution, empty when the key is not encrypted.
// Falls back to the inline SSHKeyPassphrase for the same reason
// ResolvedSSHKey does.
func (s *SignerConfig) ResolvedSSHKeyPassphrase() string {
	if s.resolvedSSHKeyPassphrase != "" {
		return s.resolvedSSHKeyPassphrase
	}
	return s.SSHKeyPassphrase
}

// Validate rejects a signer configuration that cannot issue certificates.
// Called from NewConfig for every mode. For API-only mode, no CA key source
// is required (keys come from the registry). For full and signer-only
// modes, the key requirement is checked in initSignerHandler when it
// actually tries to load the key. See keysource.NewConfigKeySource for the
// check.
//
// It also resolves ssh_key_file, so a configuration that names an
// unreadable key file fails here rather than at first signature.
func (s *SignerConfig) Validate() error {
	if err := s.PubSub.Validate(); err != nil {
		return err
	}
	if s.MaxCertLifetime <= 0 {
		return fmt.Errorf("max_cert_lifetime must be > 0, got %v", s.MaxCertLifetime)
	}
	if s.MaxServiceCertLifetime <= 0 {
		return fmt.Errorf("max_service_cert_lifetime must be > 0, got %v", s.MaxServiceCertLifetime)
	}

	if err := s.resolveCAKey(); err != nil {
		return err
	}
	return nil
}

// resolveCAKey enforces that at most one CA key source is configured and
// reads ssh_key_file when that is the one. Two sources would be ambiguous
// about which key signs, and picking a precedence order would mean a
// deployment that silently signs with the wrong CA -- so this refuses
// rather than choosing.
//
// Zero sources is allowed here: API mode has no key at all, and the full
// and signer modes report the missing key from NewConfigKeySource when they
// try to load it. See the Validate doc comment.
func (s *SignerConfig) resolveCAKey() error {
	hsmEnabled := s.HSM.Enabled()
	agentEnabled := s.Agent.Enabled()

	configured := make([]string, 0, 4)
	if s.SSHKey != "" {
		configured = append(configured, "ssh_key")
	}
	if s.SSHKeyFile != "" {
		configured = append(configured, "ssh_key_file")
	}
	if hsmEnabled {
		configured = append(configured, "hsm")
	}
	if agentEnabled {
		configured = append(configured, "ssh_key_agent")
	}
	if len(configured) > 1 {
		return fmt.Errorf("exactly one of ssh_key, ssh_key_file, hsm and ssh_key_agent may be set, got %s: two CA key sources would be ambiguous",
			strings.Join(configured, " and "))
	}

	// A passphrase decrypts PEM. Neither a token nor an agent has any PEM
	// to decrypt, so a passphrase set alongside one is a misunderstanding
	// worth naming rather than ignoring.
	if hsmEnabled || agentEnabled {
		if s.SSHKeyPassphrase != "" || s.SSHKeyPassphraseFile != "" {
			source := "hsm"
			unlock := "an HSM key is unlocked by hsm.pin, not by a passphrase"
			if agentEnabled {
				source = "ssh_key_agent"
				unlock = "an agent holds an already-unlocked key; unlock it when you ssh-add it"
			}
			return fmt.Errorf("ssh_key_passphrase cannot be used with %s: %s", source, unlock)
		}
	}

	if agentEnabled {
		return s.Agent.validate()
	}
	if hsmEnabled {
		return s.HSM.validate()
	}

	// Read at startup rather than at first signature, so an unreadable key
	// file stops the process with a message naming the path instead of
	// surfacing later as a signing error.
	s.resolvedSSHKey = s.SSHKey
	if s.SSHKeyFile != "" {
		data, err := os.ReadFile(s.SSHKeyFile)
		if err != nil {
			return fmt.Errorf("read ssh_key_file %q: %w", s.SSHKeyFile, err)
		}
		// Deliberately not trimmed: PEM delimiters are whitespace-
		// sensitive, and unlike a PIN there is no editor newline to strip.
		// An empty file is caught here rather than as "ssh: no key found".
		if len(data) == 0 {
			return fmt.Errorf("ssh_key_file %q is empty", s.SSHKeyFile)
		}
		s.resolvedSSHKey = string(data)
	}

	return s.resolveSSHKeyPassphrase()
}

// resolveSSHKeyPassphrase reads ssh_key_passphrase_file if set. A
// passphrase configured with no key at all is rejected rather than ignored:
// it almost always means the key source was meant to be set too, and a
// silently-ignored credential is the kind of thing nobody notices until the
// key it was supposed to protect turns out to be plaintext.
func (s *SignerConfig) resolveSSHKeyPassphrase() error {
	if s.SSHKeyPassphrase != "" && s.SSHKeyPassphraseFile != "" {
		return fmt.Errorf("exactly one of ssh_key_passphrase and ssh_key_passphrase_file may be set")
	}

	s.resolvedSSHKeyPassphrase = s.SSHKeyPassphrase
	if s.SSHKeyPassphraseFile != "" {
		data, err := os.ReadFile(s.SSHKeyPassphraseFile)
		if err != nil {
			return fmt.Errorf("read ssh_key_passphrase_file %q: %w", s.SSHKeyPassphraseFile, err)
		}
		// Trimmed, unlike the key itself: an editor's trailing newline is
		// never part of a passphrase, and this matches mail.smtp
		// password_file.
		s.resolvedSSHKeyPassphrase = strings.TrimSpace(string(data))
		if s.resolvedSSHKeyPassphrase == "" {
			return fmt.Errorf("ssh_key_passphrase_file %q is empty", s.SSHKeyPassphraseFile)
		}
	}

	if s.resolvedSSHKeyPassphrase != "" && s.resolvedSSHKey == "" {
		return fmt.Errorf("ssh_key_passphrase is set but no ssh_key or ssh_key_file is: a passphrase decrypts a CA key, it does not supply one")
	}
	return nil
}
