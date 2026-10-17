package config

import (
	"strings"
	"testing"
	"time"
)

// validCertificateOptions is the smallest CertificateOptions that passes
// Validate, so each test below can knock out exactly one field.
func validCertificateOptions() CertificateOptions {
	return CertificateOptions{
		ClientTimeout: 5 * time.Minute,
		Service: CertOptionsService{
			ValidDuration:      time.Hour,
			EnrollmentDuration: 8760 * time.Hour,
		},
	}
}

func TestCertificateOptions_Validate_AcceptsDefaults(t *testing.T) {
	t.Parallel()

	opts := validCertificateOptions()
	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestCertificateOptions_Validate_RejectsZeroClientTimeout(t *testing.T) {
	t.Parallel()

	opts := validCertificateOptions()
	opts.ClientTimeout = 0

	err := opts.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want an error for a zero client_timeout")
	}
	if !strings.Contains(err.Error(), "cert_options.client_timeout") {
		t.Errorf("Validate() error = %q, want it to name cert_options.client_timeout", err)
	}
}

// The budget has to stay large enough for SigningGrace to round to
// something: below signingShare it divides to zero, which would hand gocron
// a zero sweep interval and `service retrieve` a timer that fires at once.
func TestCertificateOptions_Validate_RejectsAClientTimeoutTooSmallToSplit(t *testing.T) {
	t.Parallel()

	opts := validCertificateOptions()
	opts.ClientTimeout = signingShare - 1

	if err := opts.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want an error for a client_timeout that cannot be split")
	}
}

// The two shares must account for the whole budget: the worst-case client
// wait is the stranded cutoff plus one sweep interval, and both come out of
// this number.
func TestCertificateOptions_SharesShouldAccountForTheWholeBudget(t *testing.T) {
	t.Parallel()

	opts := validCertificateOptions()
	if got := opts.ApprovalTTL() + 2*opts.SigningGrace(); got != opts.ClientTimeout {
		t.Errorf("ApprovalTTL + 2*SigningGrace = %v, want ClientTimeout %v", got, opts.ClientTimeout)
	}
}

// should reject a zero enrollment lifetime by name: left at zero, every
// enrollment code is born expired, and the only symptom downstream is
// `service retrieve` reporting an unknown code.
func TestCertificateOptions_Validate_RejectsZeroEnrollmentDuration(t *testing.T) {
	t.Parallel()

	opts := validCertificateOptions()
	opts.Service.EnrollmentDuration = 0

	err := opts.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want an error for a zero enrollment_duration")
	}
	if !strings.Contains(err.Error(), "cert_options.service.enrollment_duration") {
		t.Errorf("Validate() error = %q, want it to name cert_options.service.enrollment_duration", err)
	}
}

func TestCertificateOptions_Validate_RejectsNegativeEnrollmentDuration(t *testing.T) {
	t.Parallel()

	opts := validCertificateOptions()
	opts.Service.EnrollmentDuration = -time.Hour

	if err := opts.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want an error for a negative enrollment_duration")
	}
}

// should not tie the code's lifetime to the certificate's: a short
// valid_duration with a long enrollment_duration is the configuration the
// setting exists for, not a contradiction to reject.
func TestCertificateOptions_Validate_AcceptsEnrollmentOutlivingCertificate(t *testing.T) {
	t.Parallel()

	opts := validCertificateOptions()
	opts.Service.ValidDuration = 5 * time.Minute
	opts.Service.EnrollmentDuration = 90 * 24 * time.Hour

	if err := opts.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

// The global budget is the ceiling every per-type budget is measured
// against. Everything derived from it — the stranded-request sweep's
// cutoff, the resolved-outcome cache's eviction age, the sweep interval —
// is computed from the longest possible budget, so a type allowed to exceed
// it could have a live request failed by the sweep.
func TestCertificateOptions_Validate_RejectsAConsoleBudgetLongerThanTheGlobal(t *testing.T) {
	t.Parallel()

	opts := validCertificateOptions()
	opts.Console.ClientTimeout = opts.ClientTimeout + time.Second

	err := opts.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want an error for a console budget above the ceiling")
	}
	if !strings.Contains(err.Error(), "cert_options.console.client_timeout") {
		t.Errorf("Validate() error = %q, want it to name cert_options.console.client_timeout", err)
	}
}

func TestCertificateOptions_Validate_AcceptsAConsoleBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		console time.Duration
	}{
		{name: "unset inherits the global", console: 0},
		{name: "shorter than the global", console: 2 * time.Minute},
		{name: "exactly the global", console: 5 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := validCertificateOptions()
			opts.Console.ClientTimeout = tt.console
			if err := opts.Validate(); err != nil {
				t.Errorf("Validate() error = %v, want nil", err)
			}
		})
	}
}

// The same floor as the global, for the same reason: SigningGraceFor
// divides by signingShare, so a smaller budget rounds the machine's share
// to nothing.
func TestCertificateOptions_Validate_RejectsAConsoleBudgetTooSmallToSplit(t *testing.T) {
	t.Parallel()

	opts := validCertificateOptions()
	opts.Console.ClientTimeout = signingShare - 1

	if err := opts.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want an error for a console budget that cannot be split")
	}
}

// A gate that silently matched nothing would leave an operator believing
// console logins were restricted, so an unparseable network is a startup
// error rather than an ignored line.
func TestCertificateOptions_Validate_RejectsAnUnparseableAllowedNetwork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cidr  string
		valid bool
	}{
		{name: "an IPv4 network", cidr: "10.20.0.0/16", valid: true},
		{name: "an IPv6 network", cidr: "2001:db8::/32", valid: true},
		{name: "the whole of IPv4", cidr: "0.0.0.0/0", valid: true},
		{name: "a bare address with no prefix", cidr: "10.20.0.1"},
		{name: "a prefix out of range", cidr: "10.20.0.0/33"},
		{name: "not an address at all", cidr: "the datacentre"},
		{name: "empty", cidr: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := validCertificateOptions()
			opts.Console.AllowedNetworks = []string{tt.cidr}

			err := opts.Validate()
			if tt.valid {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() error = nil, want an error for %q", tt.cidr)
			}
			if !strings.Contains(err.Error(), "cert_options.console.allowed_networks") {
				t.Errorf("Validate() error = %q, want it to name cert_options.console.allowed_networks", err)
			}
		})
	}
}

// The extensions ceilings are what the approval page offers as toggles, so
// a critical option listed there would be offered as an extension (#193).
// Only SSH certificate extensions and name@domain vendor extensions load.
func TestCertificateOptions_Validate_ShouldAcceptOnlyExtensionNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ext     string
		wantErr string
	}{
		{name: "should accept permit-pty", ext: "permit-pty"},
		{name: "should accept permit-user-rc", ext: "permit-user-rc"},
		{name: "should accept permit-X11-forwarding", ext: "permit-X11-forwarding"},
		{name: "should accept permit-agent-forwarding", ext: "permit-agent-forwarding"},
		{name: "should accept permit-port-forwarding", ext: "permit-port-forwarding"},
		{name: "should accept a vendor extension", ext: "login@example.com"},
		{name: "should reject source-address and point at pin_source_address", ext: "source-address", wantErr: "pin_source_address"},
		{name: "should reject force-command as a critical option", ext: "force-command", wantErr: "critical option"},
		{name: "should reject verify-required as a critical option", ext: "verify-required", wantErr: "critical option"},
		{name: "should reject no-touch-required", ext: "no-touch-required", wantErr: "cannot be configured"},
		{name: "should reject a miscased extension", ext: "permit-x11-forwarding", wantErr: "not an SSH certificate extension"},
		{name: "should reject an unknown bare name", ext: "permit-everything", wantErr: "not an SSH certificate extension"},
		{name: "should reject an empty name", ext: "", wantErr: "not an SSH certificate extension"},
		{name: "should reject an @ with no domain", ext: "login@", wantErr: "not an SSH certificate extension"},
		{name: "should reject an @ with no name", ext: "@example.com", wantErr: "not an SSH certificate extension"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := validCertificateOptions()
			opts.Service.Extensions = []string{tt.ext}

			err := opts.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// Every list that names extensions is checked, under its own config path:
// a misspelt removal silently removes nothing, and a grant outside the
// defined names would otherwise only surface as an unrelated ceiling error.
func TestCertificateOptions_Validate_ShouldNameTheListHoldingABadExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		set      func(*CertificateOptions)
		wantPath string
	}{
		{
			name:     "should name a user ceiling",
			set:      func(o *CertificateOptions) { o.User.Extensions = []string{"source-address"} },
			wantPath: "cert_options.user.extensions",
		},
		{
			name:     "should name a service ceiling",
			set:      func(o *CertificateOptions) { o.Service.Extensions = []string{"permit-pty", "source-address"} },
			wantPath: "cert_options.service.extensions",
		},
		{
			name:     "should name a PAM ceiling",
			set:      func(o *CertificateOptions) { o.PAM.Extensions = []string{"force-command"} },
			wantPath: "cert_options.pam.extensions",
		},
		{
			name:     "should name a console ceiling",
			set:      func(o *CertificateOptions) { o.Console.Extensions = []string{"no-touch-required"} },
			wantPath: "cert_options.console.extensions",
		},
		{
			name: "should name default_extensions",
			set: func(o *CertificateOptions) {
				o.User.LifetimePolicy.DefaultExtensions = []string{"source-address"}
			},
			wantPath: "cert_options.user.lifetime_policy.default_extensions",
		},
		{
			name: "should name a tier's grant_extensions",
			set: func(o *CertificateOptions) {
				o.Service.LifetimePolicy.Tiers = []LifetimePolicyTier{
					{Name: "ok", GrantExtensions: []string{"permit-pty"}},
					{Name: "bad", GrantExtensions: []string{"source-address"}},
				}
			},
			wantPath: "cert_options.service.lifetime_policy.tiers[1].grant_extensions",
		},
		{
			name: "should name a source rule's removed_extensions",
			set: func(o *CertificateOptions) {
				o.User.LifetimePolicy.SourcePolicy = []SourcePolicyEntry{
					{CIDR: "10.0.0.0/8", RemovedExtensions: []string{"permit-x11-forwarding"}},
				}
			},
			wantPath: "cert_options.user.lifetime_policy.source_policy[0].removed_extensions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := validCertificateOptions()
			tt.set(&opts)

			err := opts.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantPath+":") {
				t.Fatalf("Validate() error = %v, want one naming %s", err, tt.wantPath)
			}
		})
	}
}

// ApprovalTTLFor and SigningGraceFor are what a per-type budget is split
// by, so they have to split it the same way the methods split the global
// one — otherwise a type's deadline and the bounds derived from the global
// would disagree about what a budget means.
func TestApprovalTTLFor_ShouldSplitAnyBudgetTheSameWay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		budget time.Duration
	}{
		{name: "the console default", budget: 2 * time.Minute},
		{name: "the global default", budget: 5 * time.Minute},
		{name: "an hour", budget: time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ApprovalTTLFor(tt.budget) + 2*SigningGraceFor(tt.budget); got != tt.budget {
				t.Errorf("ApprovalTTLFor + 2*SigningGraceFor = %v, want %v", got, tt.budget)
			}

			opts := CertificateOptions{ClientTimeout: tt.budget}
			if got, want := ApprovalTTLFor(tt.budget), opts.ApprovalTTL(); got != want {
				t.Errorf("ApprovalTTLFor(%v) = %v, want the method's %v", tt.budget, got, want)
			}
		})
	}
}
