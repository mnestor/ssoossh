package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// should parse and return a certificate from disk only if it is valid beyond
// the grace duration.
func TestReusableCertificateFile(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}

	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	tests := []struct {
		name            string
		setupCert       func() string
		within          time.Duration
		shouldReturnNil bool
	}{
		{
			name: "should return a certificate valid beyond the grace period",
			setupCert: func() string {
				cert := &ssh.Certificate{
					Key:         leaf.Public(),
					CertType:    ssh.UserCert,
					ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()),    //nolint:gosec
					ValidBefore: uint64(time.Now().Add(2 * time.Hour).Unix()), //nolint:gosec
				}
				if err := cert.SignCert(rand.Reader, caSigner); err != nil {
					t.Fatalf("SignCert() error = %v", err)
				}
				certData := string(ssh.MarshalAuthorizedKey(cert))
				dir := t.TempDir()
				path := filepath.Join(dir, "cert")
				if err := os.WriteFile(path, []byte(certData), 0600); err != nil {
					t.Fatalf("write cert: %v", err)
				}
				return path
			},
			within:          1 * time.Minute,
			shouldReturnNil: false,
		},
		{
			name: "should return nil when certificate is expired",
			setupCert: func() string {
				cert := &ssh.Certificate{
					Key:         leaf.Public(),
					CertType:    ssh.UserCert,
					ValidAfter:  uint64(time.Now().Add(-2 * time.Hour).Unix()), //nolint:gosec
					ValidBefore: uint64(time.Now().Add(-time.Hour).Unix()),     //nolint:gosec
				}
				if err := cert.SignCert(rand.Reader, caSigner); err != nil {
					t.Fatalf("SignCert() error = %v", err)
				}
				certData := string(ssh.MarshalAuthorizedKey(cert))
				dir := t.TempDir()
				path := filepath.Join(dir, "cert")
				if err := os.WriteFile(path, []byte(certData), 0600); err != nil {
					t.Fatalf("write cert: %v", err)
				}
				return path
			},
			within:          1 * time.Minute,
			shouldReturnNil: true,
		},
		{
			name: "should return nil when certificate expires within the grace period",
			setupCert: func() string {
				cert := &ssh.Certificate{
					Key:         leaf.Public(),
					CertType:    ssh.UserCert,
					ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()),       //nolint:gosec
					ValidBefore: uint64(time.Now().Add(30 * time.Second).Unix()), //nolint:gosec
				}
				if err := cert.SignCert(rand.Reader, caSigner); err != nil {
					t.Fatalf("SignCert() error = %v", err)
				}
				certData := string(ssh.MarshalAuthorizedKey(cert))
				dir := t.TempDir()
				path := filepath.Join(dir, "cert")
				if err := os.WriteFile(path, []byte(certData), 0600); err != nil {
					t.Fatalf("write cert: %v", err)
				}
				return path
			},
			within:          1 * time.Minute,
			shouldReturnNil: true,
		},
		{
			name: "should return nil when the file does not exist",
			setupCert: func() string {
				return filepath.Join(t.TempDir(), "nonexistent")
			},
			within:          1 * time.Minute,
			shouldReturnNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := tt.setupCert()
			cert := reusableCertificateFile(path, tt.within)
			if tt.shouldReturnNil && cert != nil {
				t.Errorf("reusableCertificateFile() = %v, want nil", cert)
			}
			if !tt.shouldReturnNil && cert == nil {
				t.Errorf("reusableCertificateFile() = nil, want certificate")
			}
		})
	}
}

// should handle missing --code flag.
func TestServiceRetrieve_MissingCode(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runServiceRetrieve(context.Background(), nil, &out, "", "/tmp/key", false, "1m")
	if err == nil || !strings.Contains(err.Error(), "--code is required") {
		t.Errorf("runServiceRetrieve() error = %v, want containing '--code is required'", err)
	}
}

// should handle missing --key flag.
func TestServiceRetrieve_MissingKey(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runServiceRetrieve(context.Background(), nil, &out, "code", "", false, "1m")
	if err == nil || !strings.Contains(err.Error(), "--key is required") {
		t.Errorf("runServiceRetrieve() error = %v, want containing '--key is required'", err)
	}
}

// should reject invalid --grace duration.
func TestServiceRetrieve_InvalidGrace(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := runServiceRetrieve(context.Background(), nil, &out, "code", "/tmp/key", false, "invalid")
	if err == nil || !strings.Contains(err.Error(), "invalid --grace duration") {
		t.Errorf("runServiceRetrieve() error = %v, want containing 'invalid --grace duration'", err)
	}
}

// signedCertText returns a certificate in authorized_keys form, valid until
// validUntil. Shared by the outcome tests below, which care only about how
// much validity is left on whatever is already on disk.
func signedCertText(t *testing.T, validUntil time.Time) string {
	t.Helper()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	cert := &ssh.Certificate{
		Key:         leaf.Public(),
		CertType:    ssh.UserCert,
		KeyId:       "svc",
		ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()), //nolint:gosec // a Unix timestamp is positive for any real date
		ValidBefore: uint64(validUntil.Unix()),                 //nolint:gosec // a Unix timestamp is positive for any real date
	}
	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		t.Fatalf("SignCert() error = %v", err)
	}
	return string(ssh.MarshalAuthorizedKey(cert))
}

// retrieveFixture wires a RootCommand around a fakeAPIClient and returns the
// --key path to drive runServiceRetrieve with.
func retrieveFixture(t *testing.T, apiClient *fakeAPIClient) (*RootCommand, string) {
	t.Helper()
	return &RootCommand{cfg: &config.Config{Server: "https://example.test"}, api: apiClient},
		filepath.Join(t.TempDir(), "svc_key")
}

// The three outcomes runServiceRetrieve has to distinguish. Degraded is the
// one that matters most: from ssh_config's Match exec a non-zero exit blocks
// the connection, so a refresh failure must not take out access that a
// still-valid certificate on disk already covers.
func TestServiceRetrieve_ShouldSkipWhenTheCertificateIsValidBeyondGrace(t *testing.T) {
	t.Parallel()

	apiClient := &fakeAPIClient{}
	root, keyPath := retrieveFixture(t, apiClient)
	if err := os.WriteFile(certificatePathFor(keyPath), []byte(signedCertText(t, time.Now().Add(2*time.Hour))), 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	var out bytes.Buffer
	if err := runServiceRetrieve(context.Background(), root, &out, "code", keyPath, false, "1m"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if apiClient.retrieveCalled {
		t.Error("expected the server not to be contacted for a certificate valid beyond the grace window")
	}
}

func TestServiceRetrieve_ShouldRefreshWhenTheCertificateIsInsideGrace(t *testing.T) {
	t.Parallel()

	apiClient := &fakeAPIClient{retrieveCert: signedCertText(t, time.Now().Add(2*time.Hour))}
	root, keyPath := retrieveFixture(t, apiClient)
	if err := os.WriteFile(certificatePathFor(keyPath), []byte(signedCertText(t, time.Now().Add(30*time.Second))), 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	var out bytes.Buffer
	if err := runServiceRetrieve(context.Background(), root, &out, "code", keyPath, false, "1m"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !apiClient.retrieveCalled {
		t.Error("expected a certificate inside the grace window to be refreshed")
	}
}

func TestServiceRetrieve_ShouldRefreshWhenForced(t *testing.T) {
	t.Parallel()

	apiClient := &fakeAPIClient{retrieveCert: signedCertText(t, time.Now().Add(2*time.Hour))}
	root, keyPath := retrieveFixture(t, apiClient)
	if err := os.WriteFile(certificatePathFor(keyPath), []byte(signedCertText(t, time.Now().Add(2*time.Hour))), 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	var out bytes.Buffer
	if err := runServiceRetrieve(context.Background(), root, &out, "code", keyPath, true, "1m"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !apiClient.retrieveCalled {
		t.Error("expected --force to refresh a certificate that was still valid")
	}
}

func TestServiceRetrieve_ShouldSucceedWhenRefreshFailsButTheCertificateStillCovers(t *testing.T) {
	t.Parallel()

	apiClient := &fakeAPIClient{retrieveErr: errors.New("server unreachable")}
	root, keyPath := retrieveFixture(t, apiClient)
	certPath := certificatePathFor(keyPath)
	existing := signedCertText(t, time.Now().Add(20*time.Minute))
	if err := os.WriteFile(certPath, []byte(existing), 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	var out bytes.Buffer
	// --grace 1h forces a refresh attempt against a certificate with only
	// 20 minutes left, so the failure lands while the file is still good.
	if err := runServiceRetrieve(context.Background(), root, &out, "code", keyPath, false, "1h"); err != nil {
		t.Fatalf("a failed refresh must not fail the command while the certificate is still valid, got: %v", err)
	}
	if !strings.Contains(out.String(), "server unreachable") {
		t.Errorf("warning %q does not name the refresh failure", out.String())
	}

	kept, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	if string(kept) != existing {
		t.Error("expected the existing certificate to be left untouched by a failed refresh")
	}
}

func TestServiceRetrieve_ShouldFailWhenRefreshFailsAndTheCertificateIsExpired(t *testing.T) {
	t.Parallel()

	apiClient := &fakeAPIClient{retrieveErr: errors.New("server unreachable")}
	root, keyPath := retrieveFixture(t, apiClient)
	if err := os.WriteFile(certificatePathFor(keyPath), []byte(signedCertText(t, time.Now().Add(-time.Minute))), 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	var out bytes.Buffer
	err := runServiceRetrieve(context.Background(), root, &out, "code", keyPath, false, "1m")
	if err == nil {
		t.Fatal("expected an expired certificate to leave a refresh failure fatal")
	}
}

func TestServiceRetrieve_ShouldFailWhenRefreshFailsAndThereIsNoCertificate(t *testing.T) {
	t.Parallel()

	apiClient := &fakeAPIClient{retrieveErr: errors.New("server unreachable")}
	root, keyPath := retrieveFixture(t, apiClient)

	var out bytes.Buffer
	err := runServiceRetrieve(context.Background(), root, &out, "code", keyPath, false, "1m")
	if err == nil {
		t.Fatal("expected a refresh failure with nothing on disk to be fatal")
	}
}

func TestServiceRetrieve_ShouldWriteTheCertificateBesideTheKey(t *testing.T) {
	t.Parallel()

	fresh := signedCertText(t, time.Now().Add(2*time.Hour))
	apiClient := &fakeAPIClient{retrieveCert: fresh}
	root, keyPath := retrieveFixture(t, apiClient)

	var out bytes.Buffer
	if err := runServiceRetrieve(context.Background(), root, &out, "code", keyPath, false, "1m"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(certificatePathFor(keyPath))
	if err != nil {
		t.Fatalf("expected the certificate at the derived path: %v", err)
	}
	if string(got) != fresh {
		t.Error("expected the retrieved certificate to be written verbatim")
	}
}
