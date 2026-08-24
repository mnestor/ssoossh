package tlsutils

// Test methodology: Table-driven tests with t.Parallel() for parallelization.
// Tests verify certificate/key pair resolution and TLSConfig's HasKeyPair
// precedence rules. Each test verifies one specific behavior.

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
	"slices"
	"testing"
	"time"
)

func TestTLSConfigHasKeyPair_ShouldRequireACompletePair(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tls  TLSConfig
		want bool
	}{
		{
			name: "should be true when certificate and key files are set",
			tls:  TLSConfig{CertificateInfo: CertificateInfo{CertificateFile: "cert.pem", PrivateKeyFile: "key.pem"}},
			want: true,
		},
		{
			name: "should be false when nothing is set",
			tls:  TLSConfig{},
			want: false,
		},
		{
			name: "should be false when the certificate file lacks a key file",
			tls:  TLSConfig{CertificateInfo: CertificateInfo{CertificateFile: "cert.pem"}},
			want: false,
		},
		{
			name: "should be false when the key file lacks a certificate file",
			tls:  TLSConfig{CertificateInfo: CertificateInfo{PrivateKeyFile: "key.pem"}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.tls.HasKeyPair(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// generateTestCert returns a self-signed certificate and key, PEM-encoded,
// for use as CertificateInfo test fixtures.
func generateTestCert(t *testing.T) (certPEM, keyPEM []byte, cert *x509.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "nlapd-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create test certificate: %v", err)
	}
	cert, err = x509.ParseCertificate(derBytes)
	if err != nil {
		t.Fatalf("failed to parse generated test certificate: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal test key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, cert
}

func TestCertificateInfo_LoadX509KeyPair(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM, _ := generateTestCert(t)

	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("failed to write cert file: %v", err)
	}
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	t.Run("should load a PEM file pair", func(t *testing.T) {
		t.Parallel()

		info := CertificateInfo{CertificateFile: certFile, PrivateKeyFile: keyFile}
		if _, err := info.LoadX509KeyPair(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("should populate the leaf so callers can inspect the certificate", func(t *testing.T) {
		t.Parallel()

		info := CertificateInfo{CertificateFile: certFile, PrivateKeyFile: keyFile}
		cert, err := info.LoadX509KeyPair()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cert.Leaf == nil {
			t.Error("expected a parsed Leaf on the loaded certificate")
		}
	})

	t.Run("should error when nothing is configured", func(t *testing.T) {
		t.Parallel()

		if _, err := (CertificateInfo{}).LoadX509KeyPair(); err == nil {
			t.Error("expected an error, got nil")
		}
	})

	t.Run("should error on an incomplete pair", func(t *testing.T) {
		t.Parallel()

		if _, err := (CertificateInfo{CertificateFile: certFile}).LoadX509KeyPair(); err == nil {
			t.Error("expected an error, got nil")
		}
	})

	t.Run("should error when the file pair fails to load", func(t *testing.T) {
		t.Parallel()

		info := CertificateInfo{CertificateFile: "/does/not/exist.pem", PrivateKeyFile: "/does/not/exist.key"}
		if _, err := info.LoadX509KeyPair(); err == nil {
			t.Error("expected an error, got nil")
		}
	})
}

// writeTestPair writes a fresh self-signed pair into a temp dir and returns
// the CertificateInfo naming it, for the Build tests below.
func writeTestPair(t *testing.T) CertificateInfo {
	t.Helper()

	certPEM, keyPEM, _ := generateTestCert(t)
	dir := t.TempDir()
	info := CertificateInfo{
		CertificateFile: filepath.Join(dir, "cert.pem"),
		PrivateKeyFile:  filepath.Join(dir, "key.pem"),
	}
	if err := os.WriteFile(info.CertificateFile, certPEM, 0o600); err != nil {
		t.Fatalf("failed to write cert file: %v", err)
	}
	if err := os.WriteFile(info.PrivateKeyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	return info
}

func TestTLSConfig_Build_ShouldResolveEveryFieldIntoTheStdConfig(t *testing.T) {
	t.Parallel()

	cfg := TLSConfig{
		CertificateInfo: writeTestPair(t),
		CipherSuites:    []string{"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"},
		TLSMinVersion:   "TLS1.3",
		CurveNames:      []string{"X25519"},
	}

	got, _, err := cfg.Build(false)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got.GetCertificate == nil {
		t.Error("expected GetCertificate to be set so renewals reach live listeners")
	}
	if got.MinVersion != tls.VersionTLS13 {
		t.Errorf("got MinVersion %d, want %d", got.MinVersion, tls.VersionTLS13)
	}
	if want := []tls.CurveID{tls.X25519}; len(got.CurvePreferences) != 1 || got.CurvePreferences[0] != want[0] {
		t.Errorf("got CurvePreferences %v, want %v", got.CurvePreferences, want)
	}
	if len(got.CipherSuites) != 1 {
		t.Errorf("got %d cipher suites, want 1", len(got.CipherSuites))
	}
	if got.ServerName != "" {
		t.Errorf("got ServerName %q, want empty -- it's a client-side field servers must ignore", got.ServerName)
	}
}

func TestTLSConfig_Build_ShouldLeaveCipherSuitesAndCurvesNilWhenEmpty(t *testing.T) {
	t.Parallel()

	got, _, err := TLSConfig{
		CertificateInfo: writeTestPair(t),
		TLSMinVersion:   "TLS1.3",
	}.Build(false)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got.CipherSuites != nil {
		t.Errorf("got CipherSuites %v, want nil so Go's defaults apply", got.CipherSuites)
	}
	if got.CurvePreferences != nil {
		t.Errorf("got CurvePreferences %v, want nil so Go's defaults apply", got.CurvePreferences)
	}
}

func TestTLSConfig_Build_ShouldError(t *testing.T) {
	t.Parallel()

	validCert := writeTestPair(t)

	tests := []struct {
		name string
		cfg  TLSConfig
	}{
		{
			name: "should error when TLSMinVersion is empty",
			cfg:  TLSConfig{CertificateInfo: validCert},
		},
		{
			name: "should error when TLSMinVersion is unrecognized",
			cfg:  TLSConfig{CertificateInfo: validCert, TLSMinVersion: "TLS99"},
		},
		{
			name: "should error when a cipher suite name is unrecognized",
			cfg:  TLSConfig{CertificateInfo: validCert, TLSMinVersion: "TLS1.3", CipherSuites: []string{"BOGUS"}},
		},
		{
			name: "should error when a curve name is unrecognized",
			cfg:  TLSConfig{CertificateInfo: validCert, TLSMinVersion: "TLS1.3", CurveNames: []string{"BOGUS"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := tt.cfg.Build(false); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}

func TestTLSConfig_Build_ShouldReturnNilConfigAndNilErrorWhenNoCertificateConfigured(t *testing.T) {
	t.Parallel()

	cfg := TLSConfig{TLSMinVersion: "TLS1.3"}

	tlsConfig, source, err := cfg.Build(false)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if tlsConfig != nil {
		t.Errorf("expected a nil *tls.Config when no certificate/key pair is configured, got %+v", tlsConfig)
	}
	if source != nil {
		t.Error("expected a nil *CertSource when no certificate/key pair is configured")
	}
}

func TestTLSConfig_Build_FIPS(t *testing.T) {
	t.Parallel()

	validCert := writeTestPair(t)

	t.Run("should default cipher suites and curves to the FIPS-approved sets when unset", func(t *testing.T) {
		t.Parallel()

		got, _, err := TLSConfig{CertificateInfo: validCert, TLSMinVersion: "TLS1.3"}.Build(true)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(got.CipherSuites) == 0 {
			t.Error("expected FIPS mode to default cipher suites rather than leave them nil")
		}
		for _, id := range got.CipherSuites {
			if !slices.Contains(fipsApprovedCipherSuites, id) {
				t.Errorf("default cipher suite %q is not FIPS-approved", tls.CipherSuiteName(id))
			}
		}
		if len(got.CurvePreferences) == 0 {
			t.Error("expected FIPS mode to default curves rather than leave them nil")
		}
		for _, id := range got.CurvePreferences {
			if !slices.Contains(fipsApprovedCurves, id) {
				t.Errorf("default curve %q is not FIPS-approved", id)
			}
		}
	})

	t.Run("should accept an explicitly configured FIPS-approved cipher suite and curve", func(t *testing.T) {
		t.Parallel()

		cfg := TLSConfig{
			CertificateInfo: validCert,
			TLSMinVersion:   "TLS1.3",
			CipherSuites:    []string{"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"},
			CurveNames:      []string{"CurveP384"},
		}
		if _, _, err := cfg.Build(true); err != nil {
			t.Errorf("expected no error for an approved cipher suite and curve, got %v", err)
		}
	})

	t.Run("should reject an explicitly configured non-FIPS-approved cipher suite", func(t *testing.T) {
		t.Parallel()

		cfg := TLSConfig{
			CertificateInfo: validCert,
			TLSMinVersion:   "TLS1.3",
			CipherSuites:    []string{"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256"},
		}
		if _, _, err := cfg.Build(true); err == nil {
			t.Error("expected ChaCha20-Poly1305 to be rejected under FIPS")
		}
	})

	t.Run("should reject an explicitly configured non-FIPS-approved curve", func(t *testing.T) {
		t.Parallel()

		cfg := TLSConfig{
			CertificateInfo: validCert,
			TLSMinVersion:   "TLS1.3",
			CurveNames:      []string{"X25519"},
		}
		if _, _, err := cfg.Build(true); err == nil {
			t.Error("expected X25519 to be rejected under FIPS")
		}
	})

	t.Run("should not restrict cipher suites or curves when fipsEnabled is false", func(t *testing.T) {
		t.Parallel()

		cfg := TLSConfig{
			CertificateInfo: validCert,
			TLSMinVersion:   "TLS1.3",
			CipherSuites:    []string{"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256"},
			CurveNames:      []string{"X25519"},
		}
		if _, _, err := cfg.Build(false); err != nil {
			t.Errorf("expected no FIPS restriction when fipsEnabled is false, got %v", err)
		}
	})
}
