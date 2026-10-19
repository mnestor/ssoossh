package api

// Test methodology: buildTLSConfig is checked field by field for each
// combination of settings, and then each combination is driven end to end
// against httptest.NewTLSServer, whose certificate is self-signed and so
// trusted only when pinned. A second, unrelated self-signed certificate
// stands in for "some other CA", and a pool built by the test stands in
// for the host's system store (see rootsOf).

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// caResponseBody is a valid GET /api/ca answer, so a successful handshake
// shows up as a successful call.
const caResponseBody = `{"data":{"ca":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIP/7mq9cQq4DRbk0doDBs9CsbCTEmTI1bSk1z8s1AdWc test-ca"},"error":null}`

// caServer is a TLS server answering every request with caResponseBody.
func caServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(caResponseBody))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// pinOf returns the base64 SHA-256 of a certificate's SubjectPublicKeyInfo,
// the form Config.TLSSPKIPins takes.
func pinOf(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// otherPin is a well-formed pin that matches no certificate in these tests.
func otherPin() string {
	sum := sha256.Sum256([]byte("not the server's key"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// writePEM writes der as a PEM certificate file and returns its path.
func writePEM(t *testing.T, der []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	return path
}

// unrelatedCA returns the DER of a fresh self-signed CA certificate that
// signed nothing the test servers present.
func unrelatedCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unrelated test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return der
}

// rootsOf returns a system-roots loader yielding a pool of certs, which
// stands in for the host's store so no test depends on the store of the
// machine it runs on.
func rootsOf(certs ...*x509.Certificate) func() (*x509.CertPool, error) {
	return func() (*x509.CertPool, error) {
		pool := x509.NewCertPool()
		for _, c := range certs {
			pool.AddCert(c)
		}
		return pool, nil
	}
}

// parseDER parses a certificate the test built.
func parseDER(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

// TestBuildTLSConfig checks the settings each combination produces.
func TestBuildTLSConfig(t *testing.T) {
	t.Parallel()

	ts := caServer(t)
	caFile := writePEM(t, ts.Certificate().Raw)
	emptyFile := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(emptyFile, []byte("no certificate here\n"), 0o600); err != nil {
		t.Fatalf("write empty CA file: %v", err)
	}
	systemPool := rootsOf(ts.Certificate())

	tests := []struct {
		name             string
		cfg              Config
		wantErr          bool
		wantRootCAs      bool
		wantSkip         bool
		wantVerifyHook   bool
		wantPinnedDirect bool
	}{
		{name: "should keep the system store when nothing is pinned", cfg: Config{}},
		{name: "should skip verification when asked and nothing is pinned", cfg: Config{SkipVerifySSL: true}, wantSkip: true},
		{name: "should trust only the CA file when one is given", cfg: Config{TLSCAFile: caFile}, wantRootCAs: true, wantPinnedDirect: true},
		{name: "should rely on the pin alone when there is no CA file", cfg: Config{TLSSPKIPins: []string{otherPin()}}, wantSkip: true, wantVerifyHook: true, wantPinnedDirect: true},
		{name: "should check both when given a CA file and pins", cfg: Config{TLSCAFile: caFile, TLSSPKIPins: []string{otherPin()}}, wantRootCAs: true, wantVerifyHook: true, wantPinnedDirect: true},
		{name: "should refuse skipping verification when pins are set", cfg: Config{SkipVerifySSL: true, TLSSPKIPins: []string{otherPin()}}, wantErr: true},
		{name: "should refuse skipping verification when a CA file is set", cfg: Config{SkipVerifySSL: true, TLSCAFile: caFile}, wantErr: true},
		{name: "should refuse a pin when it is not base64", cfg: Config{TLSSPKIPins: []string{"not base64!"}}, wantErr: true},
		{name: "should refuse a pin when it is not a SHA-256 digest", cfg: Config{TLSSPKIPins: []string{base64.StdEncoding.EncodeToString([]byte("short"))}}, wantErr: true},
		{name: "should refuse a pin when it is empty", cfg: Config{TLSSPKIPins: []string{""}}, wantErr: true},
		{name: "should refuse a CA file when it does not exist", cfg: Config{TLSCAFile: filepath.Join(t.TempDir(), "absent.pem")}, wantErr: true},
		{name: "should refuse a CA file when it holds no certificate", cfg: Config{TLSCAFile: emptyFile}, wantErr: true},
		{name: "should trust the system store when asked", cfg: Config{TLSSystemRoots: true, systemRoots: systemPool}, wantRootCAs: true, wantPinnedDirect: true},
		{name: "should check both when given the system store and pins", cfg: Config{TLSSystemRoots: true, systemRoots: systemPool, TLSSPKIPins: []string{otherPin()}}, wantRootCAs: true, wantVerifyHook: true, wantPinnedDirect: true},
		{name: "should refuse skipping verification when the system store is set", cfg: Config{SkipVerifySSL: true, TLSSystemRoots: true, systemRoots: systemPool}, wantErr: true},
		{name: "should refuse the system store when a CA file is set as well", cfg: Config{TLSCAFile: caFile, TLSSystemRoots: true, systemRoots: systemPool}, wantErr: true},
		{name: "should refuse the system store when it cannot be loaded", cfg: Config{TLSSystemRoots: true, systemRoots: func() (*x509.CertPool, error) { return nil, errors.New("no store") }}, wantErr: true},
		{name: "should refuse the system store when it holds no certificate", cfg: Config{TLSSystemRoots: true, systemRoots: rootsOf()}, wantErr: true},
		{name: "should refuse the system store when the loader returns no pool", cfg: Config{TLSSystemRoots: true, systemRoots: func() (*x509.CertPool, error) { return nil, nil }}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := buildTLSConfig(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildTLSConfig() error = %v", err)
			}
			if got.MinVersion != tls.VersionTLS13 {
				t.Errorf("MinVersion = %x, want TLS 1.3", got.MinVersion)
			}
			if (got.RootCAs != nil) != tt.wantRootCAs {
				t.Errorf("RootCAs set = %v, want %v", got.RootCAs != nil, tt.wantRootCAs)
			}
			if got.InsecureSkipVerify != tt.wantSkip {
				t.Errorf("InsecureSkipVerify = %v, want %v", got.InsecureSkipVerify, tt.wantSkip)
			}
			if (got.VerifyConnection != nil) != tt.wantVerifyHook {
				t.Errorf("VerifyConnection set = %v, want %v", got.VerifyConnection != nil, tt.wantVerifyHook)
			}
			if tt.cfg.pinned() != tt.wantPinnedDirect {
				t.Errorf("pinned() = %v, want %v", tt.cfg.pinned(), tt.wantPinnedDirect)
			}
		})
	}
}

// TestNewClient_ShouldFailWhenTheTLSSettingsAreInvalid covers NewClient
// passing a bad pin up rather than building a client that cannot connect.
func TestNewClient_ShouldFailWhenTheTLSSettingsAreInvalid(t *testing.T) {
	t.Parallel()

	if _, err := NewClient(Config{ServerURL: "https://ssh.example.test", TLSSPKIPins: []string{"x"}}); err == nil {
		t.Error("expected an error for an invalid pin, got none")
	}
}

// TestNewClient_Pinning drives each pinned combination against a real TLS
// server.
func TestNewClient_Pinning(t *testing.T) {
	t.Parallel()

	ts := caServer(t)
	serverPin := pinOf(ts.Certificate())
	serverCA := writePEM(t, ts.Certificate().Raw)
	otherDER := unrelatedCA(t)
	otherCA := writePEM(t, otherDER)
	serverRoots := rootsOf(ts.Certificate())
	otherRoots := rootsOf(parseDER(t, otherDER))

	tests := []struct {
		name         string
		cfg          Config
		wantErr      bool
		wantMismatch bool
	}{
		{name: "should connect when the pin matches", cfg: Config{TLSSPKIPins: []string{serverPin}}},
		{name: "should connect when any one of several pins matches", cfg: Config{TLSSPKIPins: []string{otherPin(), serverPin}}},
		{name: "should refuse the server when the pin does not match", cfg: Config{TLSSPKIPins: []string{otherPin()}}, wantErr: true, wantMismatch: true},
		{name: "should connect when the CA file issued the certificate", cfg: Config{TLSCAFile: serverCA}},
		{name: "should refuse the server when the CA file did not issue its certificate", cfg: Config{TLSCAFile: otherCA}, wantErr: true},
		{name: "should connect when the CA file and the pin both agree", cfg: Config{TLSCAFile: serverCA, TLSSPKIPins: []string{serverPin}}},
		{name: "should refuse the server when the CA agrees and the pin does not", cfg: Config{TLSCAFile: serverCA, TLSSPKIPins: []string{otherPin()}}, wantErr: true, wantMismatch: true},
		{name: "should refuse the server when the pin agrees and the CA does not", cfg: Config{TLSCAFile: otherCA, TLSSPKIPins: []string{serverPin}}, wantErr: true},
		{name: "should connect when the system store holds the issuing CA", cfg: Config{TLSSystemRoots: true, systemRoots: serverRoots}},
		{name: "should refuse the server when the system store does not hold its CA", cfg: Config{TLSSystemRoots: true, systemRoots: otherRoots}, wantErr: true},
		{name: "should connect when the system store and the pin both agree", cfg: Config{TLSSystemRoots: true, systemRoots: serverRoots, TLSSPKIPins: []string{serverPin}}},
		{name: "should refuse the server when the system store agrees and the pin does not", cfg: Config{TLSSystemRoots: true, systemRoots: serverRoots, TLSSPKIPins: []string{otherPin()}}, wantErr: true, wantMismatch: true},
		{name: "should refuse the server when the pin agrees and the system store does not", cfg: Config{TLSSystemRoots: true, systemRoots: otherRoots, TLSSPKIPins: []string{serverPin}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := tt.cfg
			cfg.ServerURL = ts.URL
			c, err := NewClient(cfg)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			_, err = c.GetCA(context.Background())
			if tt.wantErr != (err != nil) {
				t.Fatalf("GetCA() error = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantMismatch && !errors.Is(err, ErrPinMismatch) {
				t.Errorf("got %v, want ErrPinMismatch", err)
			}
		})
	}
}

// TestVerifySPKIPins_ShouldRefuseAServerWithNoCertificate covers a
// connection state with no leaf, which would otherwise index out of range.
func TestVerifySPKIPins_ShouldRefuseAServerWithNoCertificate(t *testing.T) {
	t.Parallel()

	if err := verifySPKIPins(tls.ConnectionState{}, nil); !errors.Is(err, ErrPinMismatch) {
		t.Errorf("got %v, want ErrPinMismatch", err)
	}
}

// TestNewTransport checks the proxy setting each mode produces, for the
// ordinary calls' transport and for the events stream's alike.
func TestNewTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		direct    bool
		wantProxy bool
	}{
		{name: "should honour the proxy environment when not pinned", direct: false, wantProxy: true},
		{name: "should ignore the proxy environment when pinned", direct: true, wantProxy: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := newTransport(nil, tt.direct).Proxy != nil; got != tt.wantProxy {
				t.Errorf("Proxy set = %v, want %v", got, tt.wantProxy)
			}
		})
	}
}

// TestNewClient_ShouldIgnoreTheProxyEnvironmentWhenPinned sets the proxy
// variables to a live proxy and checks a pinned client, by pin or by the
// system store, never uses it.
//
// The transport's Proxy field is the assertion that carries the weight:
// Go reads the proxy environment once per process and never proxies a
// loopback address, so the request below would bypass the proxy even with
// Proxy set. It stays to show the pinned client works with the variables
// set, and that the proxy saw nothing.
func TestNewClient_ShouldIgnoreTheProxyEnvironmentWhenPinned(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxied.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")

	ts := caServer(t)
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "should ignore the proxy environment when a pin is set", cfg: Config{TLSSPKIPins: []string{pinOf(ts.Certificate())}}},
		{name: "should ignore the proxy environment when the system store is trusted", cfg: Config{TLSSystemRoots: true, systemRoots: rootsOf(ts.Certificate())}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			cfg.ServerURL = ts.URL
			c, err := NewClient(cfg)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			transport, ok := c.http.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("transport is %T, want *http.Transport", c.http.Transport)
			}
			if transport.Proxy != nil {
				t.Error("a pinned client's transport consults the proxy environment")
			}
			if _, err := c.GetCA(context.Background()); err != nil {
				t.Fatalf("GetCA() error = %v", err)
			}
			if n := proxied.Load(); n != 0 {
				t.Errorf("the proxy received %d requests", n)
			}
		})
	}
}

// TestNewClient_ShouldRefuseTLS12WhenPinned drives a server that offers at
// most TLS 1.2: every pinned mode keeps the 1.3 minimum, the system store
// included, rather than negotiating down.
func TestNewClient_ShouldRefuseTLS12WhenPinned(t *testing.T) {
	t.Parallel()

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(caResponseBody))
	}))
	ts.TLS = &tls.Config{MaxVersion: tls.VersionTLS12}
	ts.StartTLS()
	t.Cleanup(ts.Close)

	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "should refuse a TLS 1.2 server when the system store is trusted", cfg: Config{TLSSystemRoots: true, systemRoots: rootsOf(ts.Certificate())}},
		{name: "should refuse a TLS 1.2 server when a pin is set", cfg: Config{TLSSPKIPins: []string{pinOf(ts.Certificate())}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := tt.cfg
			cfg.ServerURL = ts.URL
			c, err := NewClient(cfg)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			if _, err := c.GetCA(context.Background()); err == nil {
				t.Fatal("GetCA() reached a TLS 1.2 server")
			}
		})
	}
}

// TestLoadSystemRoots_ShouldUseTheSystemPoolWhenNoLoaderIsGiven compares
// the default loader with x509.SystemCertPool on this host, whatever its
// store holds: the same pool, or the same refusal of an empty or missing
// one.
func TestLoadSystemRoots_ShouldUseTheSystemPoolWhenNoLoaderIsGiven(t *testing.T) {
	t.Parallel()

	want, wantErr := x509.SystemCertPool()
	got, err := loadSystemRoots(nil)
	switch {
	case wantErr != nil || want.Equal(x509.NewCertPool()):
		if err == nil {
			t.Fatal("expected an error for a missing or empty system store, got none")
		}
	case err != nil:
		t.Fatalf("loadSystemRoots() error = %v", err)
	case !got.Equal(want):
		t.Error("loadSystemRoots(nil) is not the system pool")
	}
}

// TestAwaitCertificate_ShouldPinTheEventsStream covers the second
// transport: the events stream builds its own, and it must refuse a
// server the pin refuses rather than fall back to the system store.
func TestAwaitCertificate_ShouldPinTheEventsStream(t *testing.T) {
	t.Parallel()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: approved\ndata: {\"data\":{\"certificate\":\"ssh-ed25519-cert-v01@openssh.com AAAA\"}}\n\n"))
	}))
	t.Cleanup(ts.Close)

	tests := []struct {
		name    string
		pin     string
		wantErr bool
	}{
		{name: "should receive the outcome when the pin matches", pin: pinOf(ts.Certificate())},
		{name: "should refuse the stream when the pin does not match", pin: otherPin(), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := NewClient(Config{ServerURL: ts.URL, TLSSPKIPins: []string{tt.pin}})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			_, err = c.AwaitCertificate(context.Background(), &PendingRequest{eventsURL: ts.URL + "/api/certs/requests/x/events"})
			if tt.wantErr {
				if !errors.Is(err, ErrPinMismatch) {
					t.Errorf("got %v, want ErrPinMismatch", err)
				}
				return
			}
			if err != nil {
				t.Errorf("AwaitCertificate() error = %v", err)
			}
		})
	}
}
