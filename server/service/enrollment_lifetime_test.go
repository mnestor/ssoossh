package service

import (
	"context"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// serviceLifetimeOptions configures the split the two settings exist for: a
// certificate measured in hours, a code measured in months.
func serviceLifetimeOptions(certLifetime, codeLifetime time.Duration) config.CertificateOptions {
	return config.CertificateOptions{
		Service: config.CertOptionsService{
			ValidDuration:      certLifetime,
			EnrollmentDuration: codeLifetime,
		},
		ClientTimeout: 50 * time.Second, // signing grace = 5s
	}
}

// enrolledKeypair generates a client keypair and drives it through approval,
// returning the code and the keypair behind it.
func enrolledKeypair(t *testing.T, svc *CertRequestService) (string, *keypair.SSHKeypair) {
	t.Helper()

	clientKeypair, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate client keypair: %v", err)
	}
	clientPub, err := clientKeypair.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("failed to marshal client public key: %v", err)
	}
	return enrollService(t, svc, clientPub), clientKeypair
}

// parseCertificate pulls the *ssh.Certificate out of an authorized-keys line.
func parseCertificate(t *testing.T, certText string) *ssh.Certificate {
	t.Helper()

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(certText))
	if err != nil {
		t.Fatalf("failed to parse the delivered certificate: %v", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatalf("expected an *ssh.Certificate, got %T", pub)
	}
	return cert
}

// assertUnixWithin fails unless got is within tolerance of want. Both sides
// read the wall clock a moment apart, so an exact match would be flaky.
func assertUnixWithin(t *testing.T, label string, got uint64, want time.Time, tolerance time.Duration) {
	t.Helper()

	//nolint:gosec // Unix timestamps are non-negative; conversion is safe.
	gotUnix := int64(got)
	delta := time.Duration(gotUnix-want.Unix()) * time.Second
	if delta < -tolerance || delta > tolerance {
		t.Errorf("%s = %d (%s), want within %s of %d (%s)",
			label, got, time.Unix(gotUnix, 0).UTC(), tolerance, want.Unix(), want.UTC())
	}
}

// should bound the enrollment code by enrollment_duration, not by the
// certificate's valid_duration. Deriving the code's expiry from the
// certificate's lifetime meant a short service certificate killed the code
// within the same span, which defeats the reusable-from-cron contract.
func TestApproveServiceEnrollment_ShouldBoundCodeByEnrollmentDuration(t *testing.T) {
	t.Parallel()

	const certLifetime = time.Hour
	const codeLifetime = 90 * 24 * time.Hour

	svc := newTestCertRequestServiceWithOptions(t, serviceLifetimeOptions(certLifetime, codeLifetime))
	approvedAt := time.Now()
	enrolledKeypair(t, svc)

	var row model.Enrollment
	if err := svc.db.First(&row).Error; err != nil {
		t.Fatalf("failed to read back enrollment: %v", err)
	}

	if got := row.ExpiresAt.Sub(approvedAt); got < codeLifetime-time.Minute {
		t.Errorf("enrollment expires in %s, want the configured code lifetime of %s", got, codeLifetime)
	}
	// The specific regression: the code died with a certificate of the same age.
	if got := row.ExpiresAt.Sub(approvedAt); got < certLifetime+time.Minute {
		t.Errorf("enrollment expires in %s, which is still tied to valid_duration (%s)", got, certLifetime)
	}
	if row.CertificateDurationSeconds == nil {
		t.Fatal("CertificateDurationSeconds is nil, want the approval-time certificate lifetime recorded")
	}
	if got, want := *row.CertificateDurationSeconds, int64(certLifetime/time.Second); got != want {
		t.Errorf("CertificateDurationSeconds = %d, want %d (the approval-time certificate lifetime)", got, want)
	}
}

// should give every redemption the full certificate lifetime, including one
// that lands just before the code expires. Clipping the certificate to the
// code's remaining window is what made a long-lived code useless at its tail.
func TestEnrollmentRetrieve_ShouldNotClipCertificateToCodeExpiry(t *testing.T) {
	t.Parallel()

	const certLifetime = time.Hour

	svc := newTestCertRequestServiceWithOptions(t, serviceLifetimeOptions(certLifetime, 90*24*time.Hour))
	enrollmentSvc := newTestEnrollmentService(t, svc)
	startTestPipeline(t, svc)

	code, _ := enrolledKeypair(t, svc)

	// Stand in for a redemption near the end of a 90-day window: the code is
	// still live, but only just.
	almostExpired := time.Now().Add(time.Minute)
	if err := svc.db.Model(&model.Enrollment{}).
		Where("code = ?", code).
		Update("expires_at", almostExpired).Error; err != nil {
		t.Fatalf("failed to age the enrollment: %v", err)
	}

	certText, err := enrollmentSvc.Retrieve(context.Background(), code, "203.0.113.7")
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	cert := parseCertificate(t, certText)

	assertUnixWithin(t, "certificate ValidBefore", cert.ValidBefore, time.Now().Add(certLifetime), time.Minute)
	//nolint:gosec // Unix timestamps are non-negative; conversion is safe.
	if int64(cert.ValidBefore) <= almostExpired.Unix() {
		t.Errorf("certificate ValidBefore %d is still clipped to the code's expiry %d",
			cert.ValidBefore, almostExpired.Unix())
	}
}

// should still honor expires_at as the certificate bound for enrollments
// written before the two lifetimes were split. Those rows carry no stored
// duration, and expires_at is the only bound their approver ever agreed to.
func TestEnrollmentRetrieve_ShouldUseExpiryForPreSplitEnrollments(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, serviceLifetimeOptions(time.Hour, 90*24*time.Hour))
	enrollmentSvc := newTestEnrollmentService(t, svc)
	startTestPipeline(t, svc)

	code, _ := enrolledKeypair(t, svc)

	legacyExpiry := time.Now().Add(30 * time.Minute)
	// NULL, not zero: a pre-split row has no stored duration at all, which is
	// a different thing from an approval that computed a duration of zero.
	if err := svc.db.Model(&model.Enrollment{}).
		Where("code = ?", code).
		Updates(map[string]any{
			"expires_at":                   legacyExpiry,
			"certificate_duration_seconds": nil,
		}).Error; err != nil {
		t.Fatalf("failed to reshape the enrollment as a pre-split row: %v", err)
	}

	certText, err := enrollmentSvc.Retrieve(context.Background(), code, "203.0.113.7")
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	cert := parseCertificate(t, certText)

	//nolint:gosec // Unix timestamps are non-negative; conversion is safe.
	if got, want := int64(cert.ValidBefore), legacyExpiry.Unix(); got != want {
		t.Errorf("certificate ValidBefore = %d, want the stored expiry %d", got, want)
	}
}

// should refuse rather than fall back to the code's window when the approval
// computed a zero certificate duration. A pin-only lifetime policy does
// exactly that (docs/operations/certificate-lifetime-policy.md, footgun 5), and reading
// the zero as "no duration stored" would hand out a certificate valid for the
// whole enrollment window — the one span that must never become a
// certificate's. The signer refuses a zero-length span, so this fails closed.
func TestEnrollmentRetrieve_ShouldRefuseAZeroApprovalDuration(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, serviceLifetimeOptions(time.Hour, 90*24*time.Hour))
	enrollmentSvc := newTestEnrollmentService(t, svc)
	startTestPipeline(t, svc)

	code, _ := enrolledKeypair(t, svc)

	if err := svc.db.Model(&model.Enrollment{}).
		Where("code = ?", code).
		Update("certificate_duration_seconds", 0).Error; err != nil {
		t.Fatalf("failed to zero the stored duration: %v", err)
	}

	certText, err := enrollmentSvc.Retrieve(context.Background(), code, "203.0.113.7")
	if err == nil {
		cert := parseCertificate(t, certText)
		t.Fatalf("Retrieve() succeeded with a certificate valid until %s; want an error, "+
			"since a zero approval duration must not inherit the enrollment window",
			time.Unix(int64(cert.ValidBefore), 0).UTC()) //nolint:gosec // Unix timestamps are non-negative.
	}
}
