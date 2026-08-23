package config

import (
	"testing"
	"time"
)

// TestFIPSEnabled tests Config.FIPSEnabled predicate.
func TestFIPSEnabled_ShouldReturnFalseWhenUnset(t *testing.T) {
	t.Parallel()

	cfg := &Config{FIPS: nil}
	if cfg.FIPSEnabled() {
		t.Error("FIPSEnabled() should return false when FIPS is nil")
	}
}

// TestFIPSEnabled_ShouldReturnTrueWhenExplicitlyTrue tests when FIPS is explicitly enabled.
func TestFIPSEnabled_ShouldReturnTrueWhenExplicitlyTrue(t *testing.T) {
	t.Parallel()

	trueVal := true
	cfg := &Config{FIPS: &trueVal}
	if !cfg.FIPSEnabled() {
		t.Error("FIPSEnabled() should return true when FIPS is explicitly true")
	}
}

// TestFIPSEnabled_ShouldReturnFalseWhenExplicitlyFalse tests when FIPS is explicitly disabled.
func TestFIPSEnabled_ShouldReturnFalseWhenExplicitlyFalse(t *testing.T) {
	t.Parallel()

	falseVal := false
	cfg := &Config{FIPS: &falseVal}
	if cfg.FIPSEnabled() {
		t.Error("FIPSEnabled() should return false when FIPS is explicitly false")
	}
}

// TestAdminConfigIsAdminEnabled_ShouldReturnFalseWhenRequireGroupEmpty tests admin disabled.
func TestAdminConfigIsAdminEnabled_ShouldReturnFalseWhenRequireGroupEmpty(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{RequireGroup: ""}
	if admin.IsAdminEnabled() {
		t.Error("IsAdminEnabled() should return false when RequireGroup is empty")
	}
}

// TestAdminConfigIsAdminEnabled_ShouldReturnTrueWhenRequireGroupSet tests admin enabled.
func TestAdminConfigIsAdminEnabled_ShouldReturnTrueWhenRequireGroupSet(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{RequireGroup: "ssoossh-admins"}
	if !admin.IsAdminEnabled() {
		t.Error("IsAdminEnabled() should return true when RequireGroup is non-empty")
	}
}

// TestAdminConfigIsAuditorEnabled_ShouldReturnFalseWhenAuditorGroupEmpty tests auditor disabled.
func TestAdminConfigIsAuditorEnabled_ShouldReturnFalseWhenAuditorGroupEmpty(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{AuditorGroup: ""}
	if admin.IsAuditorEnabled() {
		t.Error("IsAuditorEnabled() should return false when AuditorGroup is empty")
	}
}

// TestAdminConfigIsAuditorEnabled_ShouldReturnTrueWhenAuditorGroupSet tests auditor enabled.
func TestAdminConfigIsAuditorEnabled_ShouldReturnTrueWhenAuditorGroupSet(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{AuditorGroup: "ssoossh-auditors"}
	if !admin.IsAuditorEnabled() {
		t.Error("IsAuditorEnabled() should return true when AuditorGroup is non-empty")
	}
}

// TestAdminConfigIsSSHServerAdminEnabled_ShouldReturnFalseWhenSSHServerAdminGroupEmpty tests disabled.
func TestAdminConfigIsSSHServerAdminEnabled_ShouldReturnFalseWhenSSHServerAdminGroupEmpty(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{SSHServerAdminGroup: ""}
	if admin.IsSSHServerAdminEnabled() {
		t.Error("IsSSHServerAdminEnabled() should return false when SSHServerAdminGroup is empty")
	}
}

// TestAdminConfigIsSSHServerAdminEnabled_ShouldReturnTrueWhenSSHServerAdminGroupSet tests enabled.
func TestAdminConfigIsSSHServerAdminEnabled_ShouldReturnTrueWhenSSHServerAdminGroupSet(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{SSHServerAdminGroup: "ssoossh-ssh-admins"}
	if !admin.IsSSHServerAdminEnabled() {
		t.Error("IsSSHServerAdminEnabled() should return true when SSHServerAdminGroup is non-empty")
	}
}

// TestAdminConfigRolesIndependent tests that roles are independent (not coupled).
func TestAdminConfigRolesIndependent(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{
		RequireGroup:        "ssoossh-admins",
		AuditorGroup:        "",
		SSHServerAdminGroup: "",
	}

	if !admin.IsAdminEnabled() {
		t.Error("admin should be enabled")
	}
	if admin.IsAuditorEnabled() {
		t.Error("auditor should not be enabled")
	}
	if admin.IsSSHServerAdminEnabled() {
		t.Error("SSH server admin should not be enabled")
	}
}

// TestCertOptionsUserStructure tests CertOptionsUser field construction.
func TestCertOptionsUserStructure(t *testing.T) {
	t.Parallel()

	opts := CertOptionsUser{
		RequireGroup:  "developers",
		ValidDuration: 30 * time.Minute,
		Extensions:    []string{"permit-pty", "permit-agent-forwarding"},
		KeyIDTemplate: "{{.Username}}-{{.Hostname}}-{{.Timestamp}}",
		LifetimePolicy: LifetimePolicy{
			Tiers: []LifetimePolicyTier{
				{
					Group:       "admin",
					MaxDuration: 8 * time.Hour,
				},
			},
		},
	}

	if opts.RequireGroup != "developers" {
		t.Errorf("RequireGroup = %q, want %q", opts.RequireGroup, "developers")
	}
	if opts.ValidDuration != 30*time.Minute {
		t.Errorf("ValidDuration = %v, want %v", opts.ValidDuration, 30*time.Minute)
	}
	if len(opts.Extensions) != 2 {
		t.Errorf("Extensions count = %d, want 2", len(opts.Extensions))
	}
	if opts.KeyIDTemplate == "" {
		t.Error("KeyIDTemplate should not be empty")
	}
}

// TestCertOptionsServiceStructure tests CertOptionsService field construction.
func TestCertOptionsServiceStructure(t *testing.T) {
	t.Parallel()

	opts := CertOptionsService{
		RequireGroup:  "services",
		ValidDuration: 24 * time.Hour,
		Extensions:    []string{"permit-agent-forwarding"},
	}

	if opts.RequireGroup != "services" {
		t.Errorf("RequireGroup = %q, want %q", opts.RequireGroup, "services")
	}
	if opts.ValidDuration != 24*time.Hour {
		t.Errorf("ValidDuration = %v, want %v", opts.ValidDuration, 24*time.Hour)
	}
	if len(opts.Extensions) != 1 {
		t.Errorf("Extensions count = %d, want 1", len(opts.Extensions))
	}
}

// TestCertOptionsStructure tests CertOptions (host) field construction.
func TestCertOptionsStructure(t *testing.T) {
	t.Parallel()

	opts := CertOptions{
		RequireGroup:  "infrastructure",
		ValidDuration: 365 * 24 * time.Hour,
		KeyIDTemplate: "host-{{.Hostname}}-{{.Year}}",
	}

	if opts.RequireGroup != "infrastructure" {
		t.Errorf("RequireGroup = %q, want %q", opts.RequireGroup, "infrastructure")
	}
	if opts.ValidDuration != 365*24*time.Hour {
		t.Errorf("ValidDuration = %v, want %v", opts.ValidDuration, 365*24*time.Hour)
	}
	if opts.KeyIDTemplate == "" {
		t.Error("KeyIDTemplate should not be empty")
	}
}

// TestCertOptionsPAMStructure tests CertOptionsPAM field construction.
func TestCertOptionsPAMStructure(t *testing.T) {
	t.Parallel()

	opts := CertOptionsPAM{
		RequireGroup:  "pam-users",
		ValidDuration: 5 * time.Minute,
	}

	if opts.RequireGroup != "pam-users" {
		t.Errorf("RequireGroup = %q, want %q", opts.RequireGroup, "pam-users")
	}
	if opts.ValidDuration != 5*time.Minute {
		t.Errorf("ValidDuration = %v, want %v", opts.ValidDuration, 5*time.Minute)
	}
}

// TestCertificateOptionsStructure tests full CertificateOptions construction.
func TestCertificateOptionsStructure(t *testing.T) {
	t.Parallel()

	opts := CertificateOptions{
		User: CertOptionsUser{
			RequireGroup:  "",
			ValidDuration: 30 * time.Minute,
			Extensions:    []string{"permit-pty"},
		},
		Service: CertOptionsService{
			RequireGroup:  "",
			ValidDuration: 24 * time.Hour,
		},
		Host: CertOptions{
			RequireGroup:  "infrastructure",
			ValidDuration: 365 * 24 * time.Hour,
		},
		PAM: CertOptionsPAM{
			RequireGroup:  "",
			ValidDuration: 5 * time.Minute,
		},
		RequestTTL:     10 * time.Minute,
		SigningTimeout: 30 * time.Second,
	}

	if opts.User.ValidDuration != 30*time.Minute {
		t.Errorf("User.ValidDuration mismatch")
	}
	if opts.Service.ValidDuration != 24*time.Hour {
		t.Errorf("Service.ValidDuration mismatch")
	}
	if opts.Host.RequireGroup != "infrastructure" {
		t.Errorf("Host.RequireGroup mismatch")
	}
	if opts.RequestTTL != 10*time.Minute {
		t.Errorf("RequestTTL mismatch")
	}
	if opts.SigningTimeout != 30*time.Second {
		t.Errorf("SigningTimeout mismatch")
	}
}

// TestHTTPSettingsStructure tests HTTPSettings field construction.
func TestHTTPSettingsStructure(t *testing.T) {
	t.Parallel()

	settings := HTTPSettings{
		ServerName: "example.com",
		Port:       8080,
		IsHTTPS:    true,
		PublicURL:  "https://example.com",
	}

	if settings.ServerName != "example.com" {
		t.Errorf("ServerName mismatch")
	}
	if settings.Port != 8080 {
		t.Errorf("Port mismatch")
	}
	if !settings.IsHTTPS {
		t.Errorf("IsHTTPS should be true")
	}
	if settings.PublicURL != "https://example.com" {
		t.Errorf("PublicURL mismatch")
	}
}

// TestDBStructure tests DB configuration field construction.
func TestDBStructure(t *testing.T) {
	t.Parallel()

	db := DB{
		Provider:      DBProviderPostgres,
		Connection:    "postgres://user:pass@localhost/ssoossh",
		MaxOpenConns:  25,
		MaxIdleConns:  5,
		RetryAttempts: 3,
		RetryInterval: 1 * time.Second,
	}

	if db.Provider != DBProviderPostgres {
		t.Errorf("Provider mismatch")
	}
	if db.Connection == "" {
		t.Error("Connection should not be empty")
	}
	if db.MaxOpenConns != 25 {
		t.Errorf("MaxOpenConns mismatch")
	}
	if db.RetryAttempts != 3 {
		t.Errorf("RetryAttempts mismatch")
	}
}

// TestBrandingSettingsStructure tests BrandingSettings construction.
func TestBrandingSettingsStructure(t *testing.T) {
	t.Parallel()

	branding := BrandingSettings{
		OrgName:     "Acme Corp",
		LogoPath:    "/var/lib/ssoossh/logo.png",
		LoginNotice: "Welcome to Acme SSO",
	}

	if branding.OrgName != "Acme Corp" {
		t.Errorf("OrgName mismatch")
	}
	if branding.LogoPath == "" {
		t.Error("LogoPath should not be empty")
	}
	if branding.LoginNotice == "" {
		t.Error("LoginNotice should not be empty")
	}
}

// TestConfigStructure tests complete Config structure construction.
func TestConfigStructure(t *testing.T) {
	t.Parallel()

	trueVal := true
	cfg := &Config{
		Logging:    AppLogging{Level: "info"},
		Traces:     true,
		Metrics:    true,
		Production: false,
		DB: DB{
			Provider:   DBProviderSqlite,
			Connection: ":memory:",
		},
		HTTP: HTTPSettings{
			ServerName: "localhost",
			Port:       8080,
			IsHTTPS:    false,
		},
		AuthConfig: OAuthConfig{
			ProviderURL: "https://example.com/.well-known/openid-configuration",
		},
		Admin: AdminConfig{
			RequireGroup: "ssoossh-admins",
			AuditorGroup: "ssoossh-auditors",
		},
		Signer: SignerConfig{SSHKey: "ssh-ed25519 AAAAC3..."},
		FIPS:   &trueVal,
	}

	if !cfg.FIPSEnabled() {
		t.Error("FIPSEnabled() should return true")
	}
	if !cfg.Admin.IsAdminEnabled() {
		t.Error("IsAdminEnabled() should return true")
	}
	if !cfg.Admin.IsAuditorEnabled() {
		t.Error("IsAuditorEnabled() should return true")
	}
	if cfg.DB.Provider != DBProviderSqlite {
		t.Errorf("DB.Provider mismatch")
	}
}
