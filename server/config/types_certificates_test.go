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
