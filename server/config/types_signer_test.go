package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSignerConfig_DefaultLifetimeLimits(t *testing.T) {
	t.Parallel()

	// Load config with defaults
	cc := newTestCommand()
	dir := t.TempDir()
	configPath := dir + "/ssoosshd.yaml"
	writeFile(t, configPath, `ssh_key: "test-key-material"`)

	if err := cc.Flags().Set("config", configPath); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify defaults
	if c.Signer.MaxCertLifetime != 2160*time.Hour {
		t.Errorf("MaxCertLifetime = %v, want 2160h", c.Signer.MaxCertLifetime)
	}
	if c.Signer.MaxServiceCertLifetime != 17544*time.Hour {
		t.Errorf("MaxServiceCertLifetime = %v, want 17544h", c.Signer.MaxServiceCertLifetime)
	}
}

func TestSignerConfig_CustomLifetimeLimits(t *testing.T) {
	t.Parallel()

	cc := newTestCommand()
	dir := t.TempDir()
	configPath := dir + "/ssoosshd.yaml"
	writeFile(t, configPath, `
ssh_key: "test-key-material"
max_cert_lifetime: 5000h
max_service_cert_lifetime: 30000h
`)

	if err := cc.Flags().Set("config", configPath); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if c.Signer.MaxCertLifetime != 5000*time.Hour {
		t.Errorf("MaxCertLifetime = %v, want 5000h", c.Signer.MaxCertLifetime)
	}
	if c.Signer.MaxServiceCertLifetime != 30000*time.Hour {
		t.Errorf("MaxServiceCertLifetime = %v, want 30000h", c.Signer.MaxServiceCertLifetime)
	}
}

func TestSignerConfig_Validate_RejectsZeroMaxCertLifetime(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:                 "test-key",
		MaxCertLifetime:        0,
		MaxServiceCertLifetime: 1 * time.Hour,
	}

	err := sc.Validate()
	if err == nil {
		t.Fatal("expected an error for zero MaxCertLifetime, got nil")
	}
}

func TestSignerConfig_Validate_RejectsNegativeMaxCertLifetime(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:                 "test-key",
		MaxCertLifetime:        -1 * time.Hour,
		MaxServiceCertLifetime: 1 * time.Hour,
	}

	err := sc.Validate()
	if err == nil {
		t.Fatal("expected an error for negative MaxCertLifetime, got nil")
	}
}

func TestSignerConfig_Validate_RejectsZeroMaxServiceCertLifetime(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:          "test-key",
		MaxCertLifetime: 1 * time.Hour,
	}

	err := sc.Validate()
	if err == nil {
		t.Fatal("expected an error for zero MaxServiceCertLifetime, got nil")
	}
}

func TestSignerConfig_Validate_RejectsNegativeMaxServiceCertLifetime(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:                 "test-key",
		MaxCertLifetime:        1 * time.Hour,
		MaxServiceCertLifetime: -1 * time.Hour,
	}

	err := sc.Validate()
	if err == nil {
		t.Fatal("expected an error for negative MaxServiceCertLifetime, got nil")
	}
}

func TestSignerConfig_Validate_AcceptsPositiveLifetimeLimits(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:                 "test-key",
		MaxCertLifetime:        2160 * time.Hour,
		PubSub:                 PubSubConfig{Backend: "gochannel"},
		MaxServiceCertLifetime: 17544 * time.Hour,
	}

	err := sc.Validate()
	if err != nil {
		t.Errorf("unexpected error for valid config: %v", err)
	}
}

func TestSignerConfigValidate_HSM(t *testing.T) {
	t.Parallel()

	validHSM := HSMConfig{
		Module:     "/usr/lib/softhsm/libsofthsm2.so",
		TokenLabel: "ca",
		PIN:        "1234",
		KeyLabel:   "ssoossh-ca",
	}
	validPubSub := PubSubConfig{Backend: "gochannel"}

	tests := []struct {
		name    string
		sshKey  string
		hsm     HSMConfig
		wantErr string
	}{
		{"should accept hsm block alone when complete", "", validHSM, ""},
		{"should reject when both ssh_key and hsm set", "PEM", validHSM, "exactly one"},
		{"should accept neither ssh_key nor hsm set", "", HSMConfig{}, ""},
		{"should reject hsm without token_label", "", HSMConfig{Module: "m", PIN: "p", KeyLabel: "k"}, "token_label"},
		{"should reject hsm with both pin and pin_file", "", HSMConfig{Module: "m", TokenLabel: "t", PIN: "p", PINFile: "/f", KeyLabel: "k"}, "exactly one"},
		{"should reject hsm with neither pin nor pin_file", "", HSMConfig{Module: "m", TokenLabel: "t", KeyLabel: "k"}, "exactly one"},
		{"should reject hsm without key_label or key_id", "", HSMConfig{Module: "m", TokenLabel: "t", PIN: "p"}, "key_label"},
		{"should reject non-hex key_id", "", HSMConfig{Module: "m", TokenLabel: "t", PIN: "p", KeyID: "zz"}, "key_id"},
		{"should accept key_id alone as hex", "", HSMConfig{Module: "m", TokenLabel: "t", PIN: "p", KeyID: "0a1b"}, ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sc := SignerConfig{
				SSHKey:                 tt.sshKey,
				HSM:                    tt.hsm,
				PubSub:                 validPubSub,
				MaxCertLifetime:        2160 * time.Hour,
				MaxServiceCertLifetime: 17544 * time.Hour,
			}
			err := sc.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestHSMConfigResolvePIN(t *testing.T) {
	t.Parallel()

	t.Run("should return inline pin when set", func(t *testing.T) {
		t.Parallel()
		h := HSMConfig{PIN: "1234"}
		pin, err := h.ResolvePIN()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if pin != "1234" {
			t.Errorf("expected pin %q, got %q", "1234", pin)
		}
	})

	t.Run("should read and trim pin_file when set", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		pinFile := dir + "/pin"
		if err := os.WriteFile(pinFile, []byte("5678\n"), 0600); err != nil {
			t.Fatalf("failed to write pin file: %v", err)
		}
		h := HSMConfig{PINFile: pinFile}
		pin, err := h.ResolvePIN()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if pin != "5678" {
			t.Errorf("expected pin %q, got %q", "5678", pin)
		}
	})

	t.Run("should error when pin_file unreadable", func(t *testing.T) {
		t.Parallel()
		h := HSMConfig{PINFile: "/nonexistent/pin"}
		_, err := h.ResolvePIN()
		if err == nil {
			t.Errorf("expected error for unreadable pin_file, got nil")
		}
		if !strings.Contains(err.Error(), "read hsm pin_file") {
			t.Errorf("expected error containing %q, got %v", "read hsm pin_file", err)
		}
	})
}

// writeKeyFile drops content at a fresh path under t.TempDir and returns it,
// so each case gets a path nothing else has touched.
func writeKeyFile(t *testing.T, name, content string) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// testCAKeyPEM is an unencrypted ECDSA P-256 key. Its only job is to be
// parseable; nothing here signs with it.
const testCAKeyPEM = `-----BEGIN OPENSSH PRIVATE KEY-----
placeholder
-----END OPENSSH PRIVATE KEY-----
`

func TestSignerConfig_ResolveCAKey_SourceExclusivity(t *testing.T) {
	t.Parallel()

	validHSM := HSMConfig{
		Module:     "/usr/lib/softhsm/libsofthsm2.so",
		TokenLabel: "ssoossh-ca",
		PIN:        "1234",
		KeyLabel:   "ssoossh-ca",
	}

	tests := []struct {
		name    string
		mutate  func(t *testing.T, s *SignerConfig)
		wantErr string
	}{
		{
			name:   "should accept no key source at all when in api mode",
			mutate: func(*testing.T, *SignerConfig) {},
		},
		{
			name: "should accept ssh_key alone",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
			},
		},
		{
			name: "should accept ssh_key_file alone",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKeyFile = writeKeyFile(t, "ca-key", testCAKeyPEM)
			},
		},
		{
			name: "should accept hsm alone",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.HSM = validHSM
			},
		},
		{
			name: "should reject when ssh_key and ssh_key_file are both set",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
				s.SSHKeyFile = writeKeyFile(t, "ca-key", testCAKeyPEM)
			},
			wantErr: "exactly one of ssh_key, ssh_key_file, hsm and ssh_key_agent",
		},
		{
			name: "should reject when ssh_key_file and hsm are both set",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKeyFile = writeKeyFile(t, "ca-key", testCAKeyPEM)
				s.HSM = validHSM
			},
			wantErr: "exactly one of ssh_key, ssh_key_file, hsm and ssh_key_agent",
		},
		{
			name: "should name every source that was set when all three are",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
				s.SSHKeyFile = writeKeyFile(t, "ca-key", testCAKeyPEM)
				s.HSM = validHSM
			},
			wantErr: "ssh_key and ssh_key_file and hsm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &SignerConfig{}
			tt.mutate(t, s)
			err := s.resolveCAKey()
			assertErrContains(t, err, tt.wantErr)
		})
	}
}

func TestSignerConfig_ResolveCAKey_FileReading(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(t *testing.T, s *SignerConfig)
		wantKey string
		wantErr string
	}{
		{
			name: "should read the key verbatim from ssh_key_file",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKeyFile = writeKeyFile(t, "ca-key", testCAKeyPEM)
			},
			wantKey: testCAKeyPEM,
		},
		{
			name: "should not trim trailing newline from the key",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKeyFile = writeKeyFile(t, "ca-key", "abc\n")
			},
			wantKey: "abc\n",
		},
		{
			name: "should pass through inline ssh_key unchanged",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
			},
			wantKey: testCAKeyPEM,
		},
		{
			name: "should report the path when ssh_key_file does not exist",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKeyFile = t.TempDir() + "/absent"
			},
			wantErr: "read ssh_key_file",
		},
		{
			name: "should reject an empty ssh_key_file",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKeyFile = writeKeyFile(t, "ca-key", "")
			},
			wantErr: "is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &SignerConfig{}
			tt.mutate(t, s)
			err := s.resolveCAKey()
			assertErrContains(t, err, tt.wantErr)
			if tt.wantErr == "" && s.ResolvedSSHKey() != tt.wantKey {
				t.Errorf("ResolvedSSHKey() = %q, want %q", s.ResolvedSSHKey(), tt.wantKey)
			}
		})
	}
}

func TestSignerConfig_ResolveCAKey_Passphrase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(t *testing.T, s *SignerConfig)
		wantPP  string
		wantErr string
	}{
		{
			name: "should use an inline passphrase",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
				s.SSHKeyPassphrase = "hunter2"
			},
			wantPP: "hunter2",
		},
		{
			name: "should read a passphrase from ssh_key_passphrase_file",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
				s.SSHKeyPassphraseFile = writeKeyFile(t, "pp", "hunter2")
			},
			wantPP: "hunter2",
		},
		{
			name: "should trim trailing whitespace from the passphrase file",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
				s.SSHKeyPassphraseFile = writeKeyFile(t, "pp", "hunter2\n")
			},
			wantPP: "hunter2",
		},
		{
			name: "should leave the passphrase empty when none is configured",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
			},
			wantPP: "",
		},
		{
			name: "should reject both passphrase and passphrase_file",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
				s.SSHKeyPassphrase = "hunter2"
				s.SSHKeyPassphraseFile = writeKeyFile(t, "pp", "hunter2")
			},
			wantErr: "exactly one of ssh_key_passphrase and ssh_key_passphrase_file",
		},
		{
			name: "should report the path when the passphrase file is missing",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
				s.SSHKeyPassphraseFile = t.TempDir() + "/absent"
			},
			wantErr: "read ssh_key_passphrase_file",
		},
		{
			name: "should reject an empty passphrase file",
			mutate: func(t *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
				s.SSHKeyPassphraseFile = writeKeyFile(t, "pp", "\n")
			},
			wantErr: "is empty",
		},
		{
			name: "should reject a passphrase with no key source",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.SSHKeyPassphrase = "hunter2"
			},
			wantErr: "no ssh_key or ssh_key_file",
		},
		{
			name: "should reject a passphrase alongside hsm",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.HSM = HSMConfig{
					Module: "/m.so", TokenLabel: "t", PIN: "1", KeyLabel: "k",
				}
				s.SSHKeyPassphrase = "hunter2"
			},
			wantErr: "cannot be used with hsm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &SignerConfig{}
			tt.mutate(t, s)
			err := s.resolveCAKey()
			assertErrContains(t, err, tt.wantErr)
			if tt.wantErr == "" && s.ResolvedSSHKeyPassphrase() != tt.wantPP {
				t.Errorf("ResolvedSSHKeyPassphrase() = %q, want %q",
					s.ResolvedSSHKeyPassphrase(), tt.wantPP)
			}
		})
	}
}

// assertErrContains folds the "want an error containing X, or want no error"
// check every table above shares.
func assertErrContains(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %q", err, want)
	}
}

// The accessors are what bootstrap calls, and a SignerConfig built as a
// struct literal never runs Validate. These pin that an inline key still
// arrives, and that a file-backed one does not appear from nowhere.
func TestSignerConfig_ResolvedAccessors_WithoutValidate(t *testing.T) {
	t.Parallel()

	t.Run("should return the inline key when Validate has not run", func(t *testing.T) {
		t.Parallel()
		s := &SignerConfig{SSHKey: testCAKeyPEM, SSHKeyPassphrase: "hunter2"}
		if got := s.ResolvedSSHKey(); got != testCAKeyPEM {
			t.Errorf("ResolvedSSHKey() = %q, want the inline key", got)
		}
		if got := s.ResolvedSSHKeyPassphrase(); got != "hunter2" {
			t.Errorf("ResolvedSSHKeyPassphrase() = %q, want %q", got, "hunter2")
		}
	})

	t.Run("should return empty for an unread ssh_key_file", func(t *testing.T) {
		t.Parallel()
		s := &SignerConfig{SSHKeyFile: "/etc/ssoossh/ca-key"}
		if got := s.ResolvedSSHKey(); got != "" {
			t.Errorf("ResolvedSSHKey() = %q, want empty so the caller fails loudly", got)
		}
	})

	t.Run("should prefer the resolved key over the inline one after Validate", func(t *testing.T) {
		t.Parallel()
		s := &SignerConfig{SSHKeyFile: writeKeyFile(t, "ca-key", "from-file\n")}
		if err := s.resolveCAKey(); err != nil {
			t.Fatalf("resolveCAKey: %v", err)
		}
		if got := s.ResolvedSSHKey(); got != "from-file\n" {
			t.Errorf("ResolvedSSHKey() = %q, want the file contents", got)
		}
	})
}

func TestAgentConfig(t *testing.T) {
	// Not parallel: the subtests use t.Setenv, which a parallel parent
	// forbids.
	tests := []struct {
		name       string
		agent      AgentConfig
		env        string
		wantSocket string
		wantErr    string
	}{
		{
			name:       "should use the configured socket",
			agent:      AgentConfig{Socket: "/run/ssoossh/agent.sock"},
			wantSocket: "/run/ssoossh/agent.sock",
		},
		{
			name:       "should fall back to SSH_AUTH_SOCK when socket is unset",
			agent:      AgentConfig{KeyFingerprint: "SHA256:abc"},
			env:        "/tmp/inherited.sock",
			wantSocket: "/tmp/inherited.sock",
		},
		{
			name:       "should prefer the configured socket over SSH_AUTH_SOCK",
			agent:      AgentConfig{Socket: "/run/explicit.sock"},
			env:        "/tmp/inherited.sock",
			wantSocket: "/run/explicit.sock",
		},
		{
			name:    "should reject when neither socket nor SSH_AUTH_SOCK is set",
			agent:   AgentConfig{KeyFingerprint: "SHA256:abc"},
			wantErr: "socket is required",
		},
		{
			name:    "should reject a fingerprint that is not SHA256",
			agent:   AgentConfig{Socket: "/s.sock", KeyFingerprint: "MD5:aa:bb"},
			wantErr: "not a SHA256 fingerprint",
		},
		{
			name:  "should accept an absent fingerprint",
			agent: AgentConfig{Socket: "/s.sock"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: t.Setenv and parallel subtests are exclusive.
			t.Setenv("SSH_AUTH_SOCK", tt.env)
			err := tt.agent.validate()
			assertErrContains(t, err, tt.wantErr)
			if tt.wantSocket != "" && tt.agent.ResolvedSocket() != tt.wantSocket {
				t.Errorf("ResolvedSocket() = %q, want %q", tt.agent.ResolvedSocket(), tt.wantSocket)
			}
		})
	}
}

func TestAgentConfig_Enabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		agent AgentConfig
		want  bool
	}{
		{"should be disabled when the block is empty", AgentConfig{}, false},
		{"should be enabled when only socket is set", AgentConfig{Socket: "/s.sock"}, true},
		{"should be enabled when only key_fingerprint is set", AgentConfig{KeyFingerprint: "SHA256:a"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.agent.Enabled(); got != tt.want {
				t.Errorf("Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSignerConfig_ResolveCAKey_AgentExclusivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(t *testing.T, s *SignerConfig)
		wantErr string
	}{
		{
			name: "should accept ssh_key_agent alone",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.Agent = AgentConfig{Socket: "/s.sock"}
			},
		},
		{
			name: "should reject ssh_key_agent alongside ssh_key",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.SSHKey = testCAKeyPEM
				s.Agent = AgentConfig{Socket: "/s.sock"}
			},
			wantErr: "ssh_key and ssh_key_agent",
		},
		{
			name: "should reject ssh_key_agent alongside hsm",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.HSM = HSMConfig{Module: "/m.so", TokenLabel: "t", PIN: "1", KeyLabel: "k"}
				s.Agent = AgentConfig{Socket: "/s.sock"}
			},
			wantErr: "hsm and ssh_key_agent",
		},
		{
			name: "should reject a passphrase alongside ssh_key_agent",
			mutate: func(_ *testing.T, s *SignerConfig) {
				s.Agent = AgentConfig{Socket: "/s.sock"}
				s.SSHKeyPassphrase = "hunter2"
			},
			wantErr: "cannot be used with ssh_key_agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &SignerConfig{}
			tt.mutate(t, s)
			assertErrContains(t, s.resolveCAKey(), tt.wantErr)
		})
	}
}
