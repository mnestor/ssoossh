package tlsutils

// Test methodology: table-driven where the behavior has several equivalent
// inputs (the failure modes of a bad reload), single-behavior tests
// otherwise. The reload tests write real PEM files to t.TempDir() rather
// than mocking the filesystem: the whole point of CertSource is that it
// re-reads files someone else rewrote, so faking that away would test
// nothing.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// generateNamedTestCert returns a self-signed certificate and key, PEM
// encoded, with commonName in the subject. The reload tests need to tell one
// certificate from another, which generateTestCert's fixed name cannot do.
func generateNamedTestCert(t *testing.T, commonName string) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

// writePair writes a certificate/key pair into dir and returns the
// CertificateInfo naming it.
func writePair(t *testing.T, dir, commonName string) CertificateInfo {
	t.Helper()

	certPEM, keyPEM := generateNamedTestCert(t, commonName)
	info := CertificateInfo{
		CertificateFile: filepath.Join(dir, "server.crt"),
		PrivateKeyFile:  filepath.Join(dir, "server.key"),
	}
	if err := os.WriteFile(info.CertificateFile, certPEM, 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(info.PrivateKeyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	return info
}

// servedCommonName returns the common name of the certificate the source
// would hand to a client, which is what every reload assertion turns on.
func servedCommonName(t *testing.T, s *CertSource) string {
	t.Helper()

	cert, err := s.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert.Leaf == nil {
		t.Fatal("expected the loaded certificate to carry a parsed Leaf")
	}

	return cert.Leaf.Subject.CommonName
}

func TestNewCertSource_ShouldLoadTheConfiguredPairOnConstruction(t *testing.T) {
	t.Parallel()

	source, err := NewCertSource(writePair(t, t.TempDir(), "initial"))
	if err != nil {
		t.Fatalf("NewCertSource: %v", err)
	}

	if got := servedCommonName(t, source); got != "initial" {
		t.Errorf("got common name %q, want %q", got, "initial")
	}
}

func TestNewCertSource_ShouldErrorWhenThePairCannotBeLoaded(t *testing.T) {
	t.Parallel()

	_, err := NewCertSource(CertificateInfo{
		CertificateFile: "/does/not/exist.crt",
		PrivateKeyFile:  "/does/not/exist.key",
	})
	if err == nil {
		t.Error("expected an error when the configured files do not exist")
	}
}

func TestCertSourceGetCertificate_ShouldErrorWhenNoCertificateIsLoaded(t *testing.T) {
	t.Parallel()

	// The zero value stands in for the window before the first successful
	// load: serving a nil certificate would panic inside crypto/tls.
	_, err := (&CertSource{}).GetCertificate(&tls.ClientHelloInfo{})
	if err == nil {
		t.Error("expected an error when no certificate has been loaded")
	}
}

func TestCertSourceReload_ShouldSwapInTheNewCertificate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source, err := NewCertSource(writePair(t, dir, "before"))
	if err != nil {
		t.Fatalf("NewCertSource: %v", err)
	}

	writePair(t, dir, "after")
	if err := source.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if got := servedCommonName(t, source); got != "after" {
		t.Errorf("got common name %q, want %q", got, "after")
	}
}

func TestCertSourceReload_ShouldKeepThePreviousCertificateWhenTheNewPairIsUnusable(t *testing.T) {
	t.Parallel()

	// Every one of these is a state the files are genuinely observed in
	// while another process rewrites them. None is a reason to stop serving
	// TLS with the certificate already in hand.
	tests := []struct {
		name    string
		corrupt func(t *testing.T, info CertificateInfo)
	}{
		{
			name: "should keep serving when the certificate file is removed",
			corrupt: func(t *testing.T, info CertificateInfo) {
				if err := os.Remove(info.CertificateFile); err != nil {
					t.Fatalf("remove certificate: %v", err)
				}
			},
		},
		{
			name: "should keep serving when the certificate is not valid pem",
			corrupt: func(t *testing.T, info CertificateInfo) {
				if err := os.WriteFile(info.CertificateFile, []byte("not a certificate"), 0o600); err != nil {
					t.Fatalf("write certificate: %v", err)
				}
			},
		},
		{
			name: "should keep serving when the certificate is written before its key",
			corrupt: func(t *testing.T, info CertificateInfo) {
				// The half-written window: a new certificate on disk while
				// the key is still the old one, so the pair does not match.
				certPEM, _ := generateNamedTestCert(t, "mismatched")
				if err := os.WriteFile(info.CertificateFile, certPEM, 0o600); err != nil {
					t.Fatalf("write certificate: %v", err)
				}
			},
		},
		{
			name: "should keep serving when the key file is truncated",
			corrupt: func(t *testing.T, info CertificateInfo) {
				if err := os.WriteFile(info.PrivateKeyFile, nil, 0o600); err != nil {
					t.Fatalf("truncate key: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			info := writePair(t, dir, "survivor")
			source, err := NewCertSource(info)
			if err != nil {
				t.Fatalf("NewCertSource: %v", err)
			}

			tt.corrupt(t, info)

			if err := source.Reload(); err == nil {
				t.Error("expected Reload to report the unusable pair")
			}
			if got := servedCommonName(t, source); got != "survivor" {
				t.Errorf("got common name %q, want the previous certificate %q", got, "survivor")
			}
		})
	}
}

func TestCertSourceReload_ShouldBeSafeAlongsideConcurrentHandshakes(t *testing.T) {
	t.Parallel()

	// GetCertificate runs on every handshake while the reload goroutine
	// writes. Run under -race to mean anything; the assertion is only that
	// a certificate is always served, never a nil or a torn read.
	dir := t.TempDir()
	source, err := NewCertSource(writePair(t, dir, "concurrent"))
	if err != nil {
		t.Fatalf("NewCertSource: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for range 50 {
			writePair(t, dir, "concurrent")
			_ = source.Reload()
		}
	}()

	go func() {
		defer wg.Done()
		for range 200 {
			cert, err := source.GetCertificate(&tls.ClientHelloInfo{})
			if err != nil || cert == nil {
				t.Errorf("GetCertificate returned (%v, %v) during reload", cert, err)
				return
			}
		}
	}()

	wg.Wait()
}

func TestTLSConfigBuild_ShouldResolveTheCertificatePerHandshake(t *testing.T) {
	t.Parallel()

	// A pinned Certificates list cannot be replaced on a listener that is
	// already serving, so Build has to hand crypto/tls a callback instead.
	cfg, source, err := TLSConfig{
		CertificateInfo: writePair(t, t.TempDir(), "served"),
		TLSMinVersion:   "TLS1.3",
	}.Build(false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if source == nil {
		t.Fatal("expected Build to return the CertSource backing the config")
	}
	if cfg.GetCertificate == nil {
		t.Error("expected Build to set tls.Config.GetCertificate")
	}
	if len(cfg.Certificates) != 0 {
		t.Errorf("got %d pinned certificates, want the list left empty", len(cfg.Certificates))
	}
}
