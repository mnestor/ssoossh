package signer

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/model"
)

// Test methodology: unit tests for the signing logic, against real
// throwaway ed25519 keys rather than fakes — signing is the one thing here
// that can't be meaningfully mocked, since the assertion that matters is
// "does the output actually verify against the CA." Tests run in parallel;
// nothing shares state.

// newTestKeySource returns a CAKeySource backed by a fresh throwaway CA key,
// plus that CA's public key for verification.
func newTestKeySource(t *testing.T) (CAKeySource, ssh.PublicKey) {
	t.Helper()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate CA keypair: %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("failed to build CA signer: %v", err)
	}
	return &staticKeySource{signer: caSigner}, caSigner.PublicKey()
}

// staticKeySource is a CAKeySource returning a fixed signer, or a fixed
// error when signer is nil.
type staticKeySource struct {
	signer ssh.Signer
	err    error
}

func (s *staticKeySource) Signer(context.Context) (ssh.Signer, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.signer, nil
}

// newTestPublicKey returns a fresh public key in authorized_keys format.
func newTestPublicKey(t *testing.T) string {
	t.Helper()

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	pub, err := kp.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	return pub
}

// newTestJob returns a valid user-certificate signing job.
func newTestJob(t *testing.T) certmsg.SigningJob {
	t.Helper()

	now := time.Now().Truncate(time.Second)
	return certmsg.SigningJob{
		RequestID:   "req-1",
		Type:        model.CertificateTypeUser,
		PublicKey:   newTestPublicKey(t),
		Principals:  []string{"alice"},
		KeyID:       "alice",
		ValidAfter:  now,
		ValidBefore: now.Add(time.Hour),
		Serial:      12345, // Pre-allocated serial
		RequestedOptions: certmsg.RequestedOptions{
			Extensions: []string{"permit-pty"},
		},
	}
}

// parseCert parses a signed certificate out of its authorized_keys form.
func parseCert(t *testing.T, certificate string) *ssh.Certificate {
	t.Helper()

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(certificate))
	if err != nil {
		t.Fatalf("failed to parse signed certificate: %v", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatalf("expected an *ssh.Certificate, got %T", pub)
	}
	return cert
}

func TestSign_ShouldProduceACertificateVerifiableAgainstTheCA(t *testing.T) {
	t.Parallel()

	ks, caPub := newTestKeySource(t)
	job := newTestJob(t)

	reply, err := Sign(context.Background(), ks, job, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cert := parseCert(t, reply.Certificate)
	if got := string(cert.SignatureKey.Marshal()); got != string(caPub.Marshal()) {
		t.Error("expected the certificate to be signed by the configured CA")
	}

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(caPub.Marshal())
		},
	}
	if err := checker.CheckCert("alice", cert); err != nil {
		t.Errorf("expected the certificate to validate for its principal, got %v", err)
	}
}

func TestSign_ShouldMapJobFieldsOntoTheCertificate(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	job := newTestJob(t)

	reply, err := Sign(context.Background(), ks, job, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cert := parseCert(t, reply.Certificate)

	if cert.CertType != ssh.UserCert {
		t.Errorf("got CertType %d, want %d (ssh.UserCert)", cert.CertType, ssh.UserCert)
	}
	if cert.KeyId != job.KeyID {
		t.Errorf("got KeyId %q, want %q", cert.KeyId, job.KeyID)
	}
	if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != "alice" {
		t.Errorf(`got ValidPrincipals %v, want ["alice"]`, cert.ValidPrincipals)
	}
	if cert.ValidAfter != uint64(job.ValidAfter.Unix()) {
		t.Errorf("got ValidAfter %d, want %d", cert.ValidAfter, job.ValidAfter.Unix())
	}
	if cert.ValidBefore != uint64(job.ValidBefore.Unix()) {
		t.Errorf("got ValidBefore %d, want %d", cert.ValidBefore, job.ValidBefore.Unix())
	}
	if _, ok := cert.Permissions.Extensions["permit-pty"]; !ok {
		t.Errorf("expected permit-pty in extensions, got %v", cert.Permissions.Extensions)
	}
	if cert.Serial == 0 {
		t.Error("expected a non-zero serial")
	}
	if reply.Serial != cert.Serial {
		t.Errorf("reply serial %d does not match certificate serial %d", reply.Serial, cert.Serial)
	}
	if reply.PublicKeyFingerprint == "" {
		t.Error("expected a public key fingerprint on the reply")
	}
}

func TestSign_ShouldUsePreAllocatedSerial(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	const expectedSerial = uint64(67890)

	job := newTestJob(t)
	job.Serial = expectedSerial
	reply, err := Sign(context.Background(), ks, job, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The signer should use the serial from the job, not generate a new one.
	if reply.Serial != expectedSerial {
		t.Errorf("expected reply serial %d, got %d", expectedSerial, reply.Serial)
	}
}

// TestSign_ShouldProduceSerialsStorableByDatabaseSQL is a regression test:
// Go's database/sql cannot bind a uint64 with the high bit set, so a
// full-width random serial made the audit-row insert fail — and with it the
// whole delivery — for roughly half of all issued certificates. Loops enough
// times that an unmasked implementation is essentially certain to trip it.
func TestSign_ShouldProduceSerialsStorableByDatabaseSQL(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	for i := range 50 {
		reply, err := Sign(context.Background(), ks, newTestJob(t), false)
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
		if reply.Serial > uint64(math.MaxInt64) {
			t.Fatalf("serial %d has the high bit set; database/sql cannot store it", reply.Serial)
		}
	}
}

// TestSign_ShouldCarryCriticalOptionsFaithfully guards the boundary that the
// signer is not a policy point: Approve currently strips these, so if they
// ever do arrive the signer must issue what it was told rather than quietly
// dropping them and producing a certificate that disagrees with what was
// approved.
func TestSign_ShouldCarryCriticalOptionsFaithfully(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	job := newTestJob(t)
	job.RequestedOptions.ForceCommand = "/usr/bin/true"
	job.RequestedOptions.SourceAddresses = []string{"10.0.0.1/32", "192.168.1.0/24"}

	reply, err := Sign(context.Background(), ks, job, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cert := parseCert(t, reply.Certificate)

	if got := cert.Permissions.CriticalOptions["force-command"]; got != "/usr/bin/true" {
		t.Errorf("got force-command %q, want %q", got, "/usr/bin/true")
	}
	want := "10.0.0.1/32,192.168.1.0/24"
	if got := cert.Permissions.CriticalOptions["source-address"]; got != want {
		t.Errorf("got source-address %q, want %q", got, want)
	}
}

func TestSign_ShouldGrantNoTouchRequiredWhenRequested(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	job := newTestJob(t)
	job.RequestedOptions.NoTouchRequired = true

	reply, err := Sign(context.Background(), ks, job, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cert := parseCert(t, reply.Certificate)

	if _, ok := cert.Permissions.Extensions["no-touch-required"]; !ok {
		t.Errorf("expected no-touch-required in extensions, got %v", cert.Permissions.Extensions)
	}
}

func TestSign_ShouldRejectUnsupportedCertificateTypes(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)

	for _, certType := range []model.CertificateType{
		model.CertificateTypeHost,
		model.CertificateTypeService,
	} {
		job := newTestJob(t)
		job.Type = certType

		_, err := Sign(context.Background(), ks, job, false)
		if err == nil {
			t.Fatalf("expected an error for certificate type %q, got nil", certType)
		}
		if got := errorCode(err); got != certmsg.ErrCodeUnsupportedType {
			t.Errorf("for %q: got error code %q, want %q", certType, got, certmsg.ErrCodeUnsupportedType)
		}
	}
}

// TestSign_ShouldIssueUserCertForPAM guards the mapping added for PAM
// certificates: they authenticate a person to a local operation (e.g.
// `sudo`), the same relationship a user certificate has to an SSH session,
// so they must produce an ssh.UserCert rather than being rejected the way
// host and service still are.
func TestSign_ShouldIssueUserCertForPAM(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	job := newTestJob(t)
	job.Type = model.CertificateTypePAM
	job.KeyID = "pam:alice"
	job.RequestedOptions = certmsg.RequestedOptions{}

	reply, err := Sign(context.Background(), ks, job, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cert := parseCert(t, reply.Certificate)

	if cert.CertType != ssh.UserCert {
		t.Errorf("got CertType %d, want %d (ssh.UserCert)", cert.CertType, ssh.UserCert)
	}
	if len(cert.Permissions.Extensions) != 0 {
		t.Errorf("expected no extensions on a PAM certificate, got %v", cert.Permissions.Extensions)
	}
}

func TestSign_ShouldRejectAnUnparseablePublicKey(t *testing.T) {
	t.Parallel()

	ks, _ := newTestKeySource(t)
	job := newTestJob(t)
	job.PublicKey = "not-a-public-key"

	_, err := Sign(context.Background(), ks, job, false)
	if err == nil {
		t.Fatal("expected an error for an unparseable public key, got nil")
	}
	if got := errorCode(err); got != certmsg.ErrCodeBadPublicKey {
		t.Errorf("got error code %q, want %q", got, certmsg.ErrCodeBadPublicKey)
	}
}

func TestSign_ShouldReportAnUnavailableCA(t *testing.T) {
	t.Parallel()

	ks := &staticKeySource{err: errors.New("ssh-agent unreachable")}

	_, err := Sign(context.Background(), ks, newTestJob(t), false)
	if err == nil {
		t.Fatal("expected an error when the CA key is unavailable, got nil")
	}
	if got := errorCode(err); got != certmsg.ErrCodeCAUnavailable {
		t.Errorf("got error code %q, want %q", got, certmsg.ErrCodeCAUnavailable)
	}
}

// should classify a signError by its code, defaulting to ErrCodeSignFailed for anything else, and unwrap to the wrapped error
func TestSignError(t *testing.T) {
	t.Parallel()

	t.Run("errorCode should return the signError's code", func(t *testing.T) {
		t.Parallel()
		err := newSignError(certmsg.ErrCodeBadPublicKey, "boom")
		if got := errorCode(err); got != certmsg.ErrCodeBadPublicKey {
			t.Errorf("errorCode() = %q, want %q", got, certmsg.ErrCodeBadPublicKey)
		}
	})

	t.Run("errorCode should default to ErrCodeSignFailed for an unclassified error", func(t *testing.T) {
		t.Parallel()
		if got := errorCode(errors.New("plain error")); got != certmsg.ErrCodeSignFailed {
			t.Errorf("errorCode() = %q, want %q", got, certmsg.ErrCodeSignFailed)
		}
	})

	t.Run("Unwrap should return the wrapped error", func(t *testing.T) {
		t.Parallel()
		wrapped := errors.New("underlying")
		se := &signError{code: certmsg.ErrCodeSignFailed, err: wrapped}
		if got := se.Unwrap(); got != wrapped {
			t.Errorf("Unwrap() = %v, want %v", got, wrapped)
		}
		if !errors.Is(se, wrapped) {
			t.Error("expected errors.Is to see through Unwrap to the wrapped error")
		}
	})
}

// brokenSigner is an ssh.Signer whose Sign method always fails, used to
// exercise Sign's cert.SignCert failure branch without a broken key.
type brokenSigner struct {
	pub ssh.PublicKey
}

func (b *brokenSigner) PublicKey() ssh.PublicKey { return b.pub }
func (b *brokenSigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return nil, errors.New("signing hardware unavailable")
}

// should classify a signing failure from the CA itself, not just from the key source
func TestSign_ShouldReportASigningFailure(t *testing.T) {
	t.Parallel()

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(kp.Private())
	if err != nil {
		t.Fatalf("failed to build CA signer: %v", err)
	}
	ks := &staticKeySource{signer: &brokenSigner{pub: caSigner.PublicKey()}}

	_, err = Sign(context.Background(), ks, newTestJob(t), false)
	if err == nil {
		t.Fatal("expected an error when the CA signer fails to sign, got nil")
	}
	if got := errorCode(err); got != certmsg.ErrCodeSignFailed {
		t.Errorf("got error code %q, want %q", got, certmsg.ErrCodeSignFailed)
	}
}

// newTestECDSAPublicKey returns a fresh P-384 ECDSA public key in
// authorized_keys format: FIPS-approved, unlike newTestPublicKey's ed25519
// output.
func newTestECDSAPublicKey(t *testing.T) string {
	t.Helper()

	kp, err := keypair.NewECDSAKeyPair(384)
	if err != nil {
		t.Fatalf("failed to generate ecdsa keypair: %v", err)
	}
	pub, err := kp.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	return pub
}

func TestSign_FIPS(t *testing.T) {
	t.Parallel()

	t.Run("should reject a non-FIPS-approved public key when fipsEnabled is true", func(t *testing.T) {
		t.Parallel()

		ks, _ := newTestKeySource(t)
		_, err := Sign(context.Background(), ks, newTestJob(t), true)
		if err == nil {
			t.Fatal("expected an error for a non-FIPS-approved (ed25519) key under fipsEnabled")
		}
		if got := errorCode(err); got != certmsg.ErrCodeFIPSNotApproved {
			t.Errorf("got error code %q, want %q", got, certmsg.ErrCodeFIPSNotApproved)
		}
	})

	t.Run("should accept a FIPS-approved public key when fipsEnabled is true", func(t *testing.T) {
		t.Parallel()

		ks, _ := newTestKeySource(t)
		job := newTestJob(t)
		job.PublicKey = newTestECDSAPublicKey(t)

		if _, err := Sign(context.Background(), ks, job, true); err != nil {
			t.Errorf("unexpected error for a FIPS-approved (ecdsa) key under fipsEnabled: %v", err)
		}
	})

	t.Run("should not restrict the key algorithm when fipsEnabled is false", func(t *testing.T) {
		t.Parallel()

		ks, _ := newTestKeySource(t)
		if _, err := Sign(context.Background(), ks, newTestJob(t), false); err != nil {
			t.Errorf("unexpected error for an ed25519 key when fipsEnabled is false: %v", err)
		}
	})
}

func TestNewConfigKeySource_ShouldRejectEmptyAndInvalidKeys(t *testing.T) {
	t.Parallel()

	if _, err := NewConfigKeySource(""); err == nil {
		t.Error("expected an error for an empty key, got nil")
	}
	if _, err := NewConfigKeySource("not a private key"); err == nil {
		t.Error("expected an error for an invalid key, got nil")
	}
}

func TestNewConfigKeySource_ShouldReturnTheParsedSigner(t *testing.T) {
	t.Parallel()

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	pem, err := kp.MarshalPrivateKey()
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}

	ks, err := NewConfigKeySource(string(pem))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	signer, err := ks.Signer(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signer == nil {
		t.Fatal("expected a non-nil signer")
	}

	// The signer must correspond to the key we handed in.
	want, err := kp.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	got := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Errorf("signer public key %q does not match the configured key %q", got, want)
	}
}
