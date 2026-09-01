package service

// Test methodology: unit tests over the TLS half of the directory dialer.
// The security posture lives here — TLS 1.2 minimum, verification on by
// default, and a CA file that fails closed — so each branch is pinned.
// dialLDAP's happy path needs a live directory and is exercised by the
// tagged integration suites; its fail-before-network branches are unit
// tested below.

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
)

// selfSignedCA is a minimal PEM certificate, enough for AppendCertsFromPEM
// to accept. Generated once for tests; the content never verifies anything.
const selfSignedCA = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----`

func TestLDAPTLSConfig(t *testing.T) {
	t.Parallel()

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, []byte(selfSignedCA), 0o600); err != nil {
		t.Fatalf("writing the CA file: %v", err)
	}

	tests := []struct {
		name     string
		cfg      config.LDAPConfig
		wantText string
		check    func(*testing.T, *tls.Config)
	}{
		{
			name: "the default verifies with TLS 1.2 minimum",
			cfg:  config.LDAPConfig{},
			check: func(t *testing.T, got *tls.Config) {
				if got.MinVersion != tls.VersionTLS12 {
					t.Errorf("MinVersion = %x, want TLS 1.2", got.MinVersion)
				}
				if got.InsecureSkipVerify {
					t.Error("InsecureSkipVerify defaulted on")
				}
				if got.RootCAs != nil {
					t.Error("RootCAs set with no ldap.tls_ca configured")
				}
			},
		},
		{
			name: "the explicit operator opt-out is honored",
			cfg:  config.LDAPConfig{TLSInsecureSkipVerify: true},
			check: func(t *testing.T, got *tls.Config) {
				if !got.InsecureSkipVerify {
					t.Error("InsecureSkipVerify = false despite the opt-out")
				}
			},
		},
		{
			name: "a configured CA becomes the root pool",
			cfg:  config.LDAPConfig{TLSCA: caFile},
			check: func(t *testing.T, got *tls.Config) {
				if got.RootCAs == nil {
					t.Error("RootCAs = nil, want the configured CA loaded")
				}
			},
		},
		{
			name:     "a missing CA file fails closed",
			cfg:      config.LDAPConfig{TLSCA: filepath.Join(t.TempDir(), "absent.pem")},
			wantText: "ldap.tls_ca",
		},
		{
			name:     "a CA file with no certificates fails closed",
			cfg:      config.LDAPConfig{TLSCA: writeTempFile(t, "not a certificate")},
			wantText: "no usable certificates",
		},
	}

	// By index: LDAPConfig embeds a sync.Once via its logging block, so the
	// rows must not be copied.
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ldapTLSConfig(&tt.cfg)
			if tt.wantText != "" {
				if err == nil {
					t.Fatalf("ldapTLSConfig() error = nil, want one mentioning %q", tt.wantText)
				}
				if !strings.Contains(err.Error(), tt.wantText) {
					t.Errorf("ldapTLSConfig() error = %q, want it to mention %q", err, tt.wantText)
				}
				return
			}
			if err != nil {
				t.Fatalf("ldapTLSConfig() error = %v", err)
			}
			tt.check(t, got)
		})
	}
}

// writeTempFile writes content to a fresh temp file and returns its path.
func writeTempFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	return path
}

// A bad CA must stop the dial before any network happens.
func TestDialLDAP_ShouldFailClosedOnABadCA(t *testing.T) {
	t.Parallel()

	cfg := config.LDAPConfig{
		URL:   "ldaps://directory.invalid",
		TLSCA: filepath.Join(t.TempDir(), "absent.pem"),
	}
	if _, err := dialLDAP(&cfg); err == nil {
		t.Error("dialLDAP() with an unreadable CA returned no error")
	}
}

// An unreachable directory reports the connection failure. Port 1 on
// loopback refuses immediately, so this stays fast and offline.
func TestDialLDAP_ShouldReportAConnectionFailure(t *testing.T) {
	t.Parallel()

	cfg := config.LDAPConfig{URL: "ldap://127.0.0.1:1", Timeout: time.Second}
	_, err := dialLDAP(&cfg)
	if err == nil {
		t.Fatal("dialLDAP() to a refusing port returned no error")
	}
	if !strings.Contains(err.Error(), "failed to connect") {
		t.Errorf("dialLDAP() error = %q, want the connect failure named", err)
	}
}
