package config

import (
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
	}

	err := sc.Validate()
	if err != nil {
		t.Errorf("unexpected error for valid config: %v", err)
	}
}
