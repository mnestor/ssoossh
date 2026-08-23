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
	if c.Signer.MaxHostCertLifetime != 17544*time.Hour {
		t.Errorf("MaxHostCertLifetime = %v, want 17544h", c.Signer.MaxHostCertLifetime)
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
max_host_cert_lifetime: 20000h
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
	if c.Signer.MaxHostCertLifetime != 20000*time.Hour {
		t.Errorf("MaxHostCertLifetime = %v, want 20000h", c.Signer.MaxHostCertLifetime)
	}
}

func TestSignerConfig_Validate_RejectsZeroMaxCertLifetime(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:              "test-key",
		MaxCertLifetime:     0,
		MaxHostCertLifetime: 1 * time.Hour,
	}

	err := sc.Validate()
	if err == nil {
		t.Fatal("expected an error for zero MaxCertLifetime, got nil")
	}
}

func TestSignerConfig_Validate_RejectsNegativeMaxCertLifetime(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:              "test-key",
		MaxCertLifetime:     -1 * time.Hour,
		MaxHostCertLifetime: 1 * time.Hour,
	}

	err := sc.Validate()
	if err == nil {
		t.Fatal("expected an error for negative MaxCertLifetime, got nil")
	}
}

func TestSignerConfig_Validate_RejectsZeroMaxHostCertLifetime(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:              "test-key",
		MaxCertLifetime:     1 * time.Hour,
		MaxHostCertLifetime: 0,
	}

	err := sc.Validate()
	if err == nil {
		t.Fatal("expected an error for zero MaxHostCertLifetime, got nil")
	}
}

func TestSignerConfig_Validate_RejectsNegativeMaxHostCertLifetime(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:              "test-key",
		MaxCertLifetime:     1 * time.Hour,
		MaxHostCertLifetime: -1 * time.Hour,
	}

	err := sc.Validate()
	if err == nil {
		t.Fatal("expected an error for negative MaxHostCertLifetime, got nil")
	}
}

func TestSignerConfig_Validate_AcceptsPositiveLifetimeLimits(t *testing.T) {
	t.Parallel()

	sc := SignerConfig{
		SSHKey:              "test-key",
		MaxCertLifetime:     2160 * time.Hour,
		MaxHostCertLifetime: 17544 * time.Hour,
		PubSub:              PubSubConfig{Backend: "gochannel"},
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
				SSHKey:              tt.sshKey,
				HSM:                 tt.hsm,
				PubSub:              validPubSub,
				MaxCertLifetime:     2160 * time.Hour,
				MaxHostCertLifetime: 17544 * time.Hour,
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
