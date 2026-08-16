package tlsutils

// Test methodology: Table-driven tests with t.Parallel() for parallelization.
// Tests verify TLS cipher suite name resolution and crypto/tls integration.
// Each test verifies one specific parsing or validation behavior.

import (
	"crypto/tls"
	"testing"
)

func TestCipherSuites_ShouldResolveKnownNames(t *testing.T) {
	t.Parallel()

	suites, err := CipherSuites([]string{"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(suites) != 1 {
		t.Fatalf("expected 1 suite, got %d", len(suites))
	}
}

func TestCipherSuites_ShouldReturnCorrectID(t *testing.T) {
	t.Parallel()

	suites, err := CipherSuites([]string{"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var want uint16
	for _, cs := range tls.CipherSuites() {
		if cs.Name == "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384" {
			want = cs.ID
		}
	}
	if suites[0] != want {
		t.Errorf("got ID %d, want %d", suites[0], want)
	}
}

func TestCipherSuites_ShouldErrorOnInsecureSuite(t *testing.T) {
	t.Parallel()

	insecure := tls.InsecureCipherSuites()
	if len(insecure) == 0 {
		t.Skip("no insecure cipher suites known to this Go version")
	}

	_, err := CipherSuites([]string{insecure[0].Name})
	if err == nil {
		t.Fatal("expected an error for an insecure cipher suite name, got nil")
	}
}

func TestCipherSuites_ShouldErrorWhenNameUnknown(t *testing.T) {
	t.Parallel()

	_, err := CipherSuites([]string{"NOT_A_REAL_CIPHER_SUITE"})
	if err == nil {
		t.Fatal("expected an error for unknown cipher suite name, got nil")
	}
}

func TestCipherSuites_ShouldErrorWhenNameWrongCase(t *testing.T) {
	t.Parallel()

	// Unlike Curve and MinVersion, CipherSuites does not normalize case,
	// so a lowercase name should fail to resolve.
	_, err := CipherSuites([]string{"tls_ecdhe_rsa_with_aes_256_gcm_sha384"})
	if err == nil {
		t.Fatal("expected an error for wrong-case cipher suite name, got nil")
	}
}

func TestCipherSuites_ShouldReturnNilWhenInputEmpty(t *testing.T) {
	t.Parallel()

	// Must be nil, not an empty slice: a non-nil tls.Config.CipherSuites is
	// validated by net/http's HTTP/2 setup, which rejects an empty list.
	suites, err := CipherSuites([]string{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if suites != nil {
		t.Errorf("expected nil, got %v", suites)
	}
}

func TestCipherSuites_ShouldReturnNilWhenInputNil(t *testing.T) {
	t.Parallel()

	suites, err := CipherSuites(nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if suites != nil {
		t.Errorf("expected nil, got %v", suites)
	}
}

func TestCipherSuites_ShouldStopAtFirstUnknownName(t *testing.T) {
	t.Parallel()

	_, err := CipherSuites([]string{"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384", "BOGUS"})
	if err == nil {
		t.Fatal("expected an error when one of several names is unknown, got nil")
	}
}

func TestCipherSuites_ShouldDedupeWhilePreservingFirstOccurrenceOrder(t *testing.T) {
	t.Parallel()

	const (
		a = "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"
		b = "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
	)

	suites, err := CipherSuites([]string{b, a, b, a})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var wantA, wantB uint16
	for _, cs := range tls.CipherSuites() {
		switch cs.Name {
		case a:
			wantA = cs.ID
		case b:
			wantB = cs.ID
		}
	}
	want := []uint16{wantB, wantA}

	if len(suites) != len(want) {
		t.Fatalf("got %d suites, want %d: %v", len(suites), len(want), suites)
	}
	for i := range want {
		if suites[i] != want[i] {
			t.Errorf("index %d: got %d, want %d", i, suites[i], want[i])
		}
	}
}
