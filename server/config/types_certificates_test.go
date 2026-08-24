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
