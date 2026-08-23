package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

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
					ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()), //nolint:gosec
					ValidBefore: uint64(time.Now().Add(2 * time.Hour).Unix()),  //nolint:gosec
				}
				if err := cert.SignCert(rand.Reader, caSigner); err != nil {
					t.Fatalf("SignCert() error = %v", err)
				}
				certData := string(ssh.MarshalAuthorizedKey(cert))
				dir := t.TempDir()
				path := filepath.Join(dir, "cert")
				if err := os.WriteFile(path, []byte(certData), 0644); err != nil {
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
					ValidBefore: uint64(time.Now().Add(-time.Hour).Unix()),  //nolint:gosec
				}
				if err := cert.SignCert(rand.Reader, caSigner); err != nil {
					t.Fatalf("SignCert() error = %v", err)
				}
				certData := string(ssh.MarshalAuthorizedKey(cert))
				dir := t.TempDir()
				path := filepath.Join(dir, "cert")
				if err := os.WriteFile(path, []byte(certData), 0644); err != nil {
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
					ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()), //nolint:gosec
					ValidBefore: uint64(time.Now().Add(30 * time.Second).Unix()),  //nolint:gosec
				}
				if err := cert.SignCert(rand.Reader, caSigner); err != nil {
					t.Fatalf("SignCert() error = %v", err)
				}
				certData := string(ssh.MarshalAuthorizedKey(cert))
				dir := t.TempDir()
				path := filepath.Join(dir, "cert")
				if err := os.WriteFile(path, []byte(certData), 0644); err != nil {
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

