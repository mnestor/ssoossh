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

// TestAdminConfigIsSOCEnabled_ShouldReturnFalseWhenSOCGroupEmpty tests SOC disabled.
func TestAdminConfigIsSOCEnabled_ShouldReturnFalseWhenSOCGroupEmpty(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{SOCGroup: ""}
	if admin.IsSOCEnabled() {
		t.Error("IsSOCEnabled() should return false when SOCGroup is empty")
	}
}

// TestAdminConfigIsSOCEnabled_ShouldReturnTrueWhenSOCGroupSet tests SOC enabled.
func TestAdminConfigIsSOCEnabled_ShouldReturnTrueWhenSOCGroupSet(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{SOCGroup: "ssoossh-soc"}
	if !admin.IsSOCEnabled() {
		t.Error("IsSOCEnabled() should return true when SOCGroup is non-empty")
	}
}

// TestAdminConfigRolesIndependent tests that roles are independent (not coupled).
func TestAdminConfigRolesIndependent(t *testing.T) {
	t.Parallel()

	admin := &AdminConfig{
		RequireGroup: "ssoossh-admins",
		AuditorGroup: "",
	}

	if !admin.IsAdminEnabled() {
		t.Error("admin should be enabled")
	}
	if admin.IsAuditorEnabled() {
		t.Error("auditor should not be enabled")
	}
}

// TestCertOptionsUserStructure tests CertOptionsUser field construction.
func TestCertOptionsUserStructure(t *testing.T) {
	t.Parallel()

	opts := CertOptionsUser{
		Require:       &PolicyCondition{Group: "developers"},
		ValidDuration: 30 * time.Minute,
		Extensions:    []string{"permit-pty", "permit-agent-forwarding"},
		KeyIDTemplate: "{{.Username}}-{{.Hostname}}-{{.Timestamp}}",
		LifetimePolicy: LifetimePolicy{
			Tiers: []LifetimePolicyTier{
				{
					Name:        "admin",
					When:        PolicyCondition{Group: "admin"},
					MaxDuration: 8 * time.Hour,
				},
			},
		},
	}

	if opts.Require.Group != "developers" {
		t.Errorf("Require.Group = %q, want %q", opts.Require.Group, "developers")
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
		PAM: CertOptionsPAM{
			RequireGroup:  "",
			ValidDuration: 5 * time.Minute,
		},
		ClientTimeout: 10 * time.Minute,
	}

	if opts.User.ValidDuration != 30*time.Minute {
		t.Errorf("User.ValidDuration mismatch")
	}
	if opts.Service.ValidDuration != 24*time.Hour {
		t.Errorf("Service.ValidDuration mismatch")
	}
	if opts.ClientTimeout != 10*time.Minute {
		t.Errorf("ClientTimeout mismatch")
	}
	if opts.SigningGrace() != time.Minute {
		t.Errorf("SigningGrace mismatch")
	}
}

// TestHTTPSettingsStructure tests HTTPSettings field construction.
func TestHTTPSettingsStructure(t *testing.T) {
	t.Parallel()

	settings := HTTPSettings{
		Port:      8080,
		PublicURL: "https://example.com",
	}

	if settings.Port != 8080 {
		t.Errorf("Port mismatch")
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
			PublicURL: "http://localhost:8080",
			Port:      8080,
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

// GrantsAuditor is the single authority for auditor-level access --
// middleware.AuditorAuthMiddleware and every auditor-visible read go through
// it -- and it sat at 0% coverage along with containsGroup.
//
// Two rules make the table below worth reading as a whole rather than as
// separate cases. Admins are a superset of auditors, so admin membership
// grants access even when auditor_group is unset: leaving it unset narrows
// auditor operations to admins rather than locking everyone out. And it
// fails closed -- an unconfigured group must never match, or a deployment
// that simply has not set auditor_group would grant auditor access to
// anyone whose group list happens to contain an empty string.
func TestGrantsAuditor_ShouldDecideAuditorAccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    AdminConfig
		groups []string
		want   bool
	}{
		{
			name:   "admin group grants auditor access",
			cfg:    AdminConfig{RequireGroup: "admins", AuditorGroup: "auditors"},
			groups: []string{"admins"},
			want:   true,
		},
		{
			name:   "auditor group grants auditor access",
			cfg:    AdminConfig{RequireGroup: "admins", AuditorGroup: "auditors"},
			groups: []string{"auditors"},
			want:   true,
		},
		{
			name:   "admin group still grants when auditor is unconfigured",
			cfg:    AdminConfig{RequireGroup: "admins"},
			groups: []string{"admins"},
			want:   true,
		},
		{
			name:   "neither group denies",
			cfg:    AdminConfig{RequireGroup: "admins", AuditorGroup: "auditors"},
			groups: []string{"engineering"},
			want:   false,
		},
		{
			name:   "no groups at all denies",
			cfg:    AdminConfig{RequireGroup: "admins", AuditorGroup: "auditors"},
			groups: nil,
			want:   false,
		},
		{
			name:   "an empty group list entry never matches an unset admin group",
			cfg:    AdminConfig{AuditorGroup: "auditors"},
			groups: []string{""},
			want:   false,
		},
		{
			name:   "an empty group list entry never matches an unset auditor group",
			cfg:    AdminConfig{RequireGroup: "admins"},
			groups: []string{""},
			want:   false,
		},
		{
			name:   "nothing configured denies everyone",
			cfg:    AdminConfig{},
			groups: []string{"admins", "auditors", ""},
			want:   false,
		},
		{
			name:   "auditor group alone grants without an admin group",
			cfg:    AdminConfig{AuditorGroup: "auditors"},
			groups: []string{"auditors"},
			want:   true,
		},
		{
			name:   "membership is exact, not a prefix",
			cfg:    AdminConfig{RequireGroup: "admins", AuditorGroup: "auditors"},
			groups: []string{"admins-readonly", "auditors-emeritus"},
			want:   false,
		},
		{
			name:   "SOC group grants auditor access",
			cfg:    AdminConfig{RequireGroup: "admins", SOCGroup: "soc", AuditorGroup: "auditors"},
			groups: []string{"soc"},
			want:   true,
		},
		{
			name:   "SOC group still grants when auditor is unconfigured",
			cfg:    AdminConfig{RequireGroup: "admins", SOCGroup: "soc"},
			groups: []string{"soc"},
			want:   true,
		},
		{
			name:   "an empty group list entry never matches an unset SOC group",
			cfg:    AdminConfig{AuditorGroup: "auditors"},
			groups: []string{""},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.GrantsAuditor(tt.groups); got != tt.want {
				t.Errorf("GrantsAuditor(%v) = %v, want %v", tt.groups, got, tt.want)
			}
		})
	}
}

// TestGrantsSOC_ShouldDecideSOCAccess exercises the SOC containment-role
// grant. Admins are a superset of SOC, so admin membership grants access
// even when soc_group is unset, while auditor membership never does: SOC
// guards writes (disable, expire) that a read-only role must not reach. It
// fails closed the same way GrantsAuditor does — an unconfigured group must
// never match an empty group-list entry.
func TestGrantsSOC_ShouldDecideSOCAccess(t *testing.T) {
	tests := []struct {
		name   string
		cfg    AdminConfig
		groups []string
		want   bool
	}{
		{
			name:   "admin group grants SOC access",
			cfg:    AdminConfig{RequireGroup: "admins", SOCGroup: "soc"},
			groups: []string{"admins"},
			want:   true,
		},
		{
			name:   "SOC group grants SOC access",
			cfg:    AdminConfig{RequireGroup: "admins", SOCGroup: "soc"},
			groups: []string{"soc"},
			want:   true,
		},
		{
			name:   "admin group still grants when SOC is unconfigured",
			cfg:    AdminConfig{RequireGroup: "admins"},
			groups: []string{"admins"},
			want:   true,
		},
		{
			name:   "SOC group alone grants without an admin group",
			cfg:    AdminConfig{SOCGroup: "soc"},
			groups: []string{"soc"},
			want:   true,
		},
		{
			name:   "auditor group never grants SOC access",
			cfg:    AdminConfig{RequireGroup: "admins", SOCGroup: "soc", AuditorGroup: "auditors"},
			groups: []string{"auditors"},
			want:   false,
		},
		{
			name:   "neither group denies",
			cfg:    AdminConfig{RequireGroup: "admins", SOCGroup: "soc"},
			groups: []string{"engineering"},
			want:   false,
		},
		{
			name:   "no groups at all denies",
			cfg:    AdminConfig{RequireGroup: "admins", SOCGroup: "soc"},
			groups: nil,
			want:   false,
		},
		{
			name:   "an empty group list entry never matches an unset SOC group",
			cfg:    AdminConfig{RequireGroup: "admins"},
			groups: []string{""},
			want:   false,
		},
		{
			name:   "nothing configured denies everyone",
			cfg:    AdminConfig{},
			groups: []string{"admins", "soc", ""},
			want:   false,
		},
		{
			name:   "membership is exact, not a prefix",
			cfg:    AdminConfig{RequireGroup: "admins", SOCGroup: "soc"},
			groups: []string{"admins-readonly", "soc-emeritus"},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.GrantsSOC(tt.groups); got != tt.want {
				t.Errorf("GrantsSOC(%v) = %v, want %v", tt.groups, got, tt.want)
			}
		})
	}
}
