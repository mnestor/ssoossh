package bootstrap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
)

// Test methodology: Integration tests that start a real Server with a real TCP
// listener and make actual HTTP/HTTPS requests. Cannot run in parallel (they
// bind ephemeral ports and manage certificates). Uses context cancellation for
// graceful shutdown. Real certificates are generated on-the-fly via helper
// functions. See router_test.go for unit tests of individual middleware/routes.

func TestInitRouter_ShouldRegisterPingRoute(t *testing.T) {
	t.Parallel()

	a := newTestApp(t, &config.Config{})

	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "pong" {
		t.Errorf("got body %q, want %q", w.Body.String(), "pong")
	}
}

func TestInitRouter_ShouldServeRequestsWhenTracesEnabled(t *testing.T) {
	t.Parallel()

	c := &config.Config{Traces: true}
	a := newTestApp(t, c)

	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// The otelgin middleware runs against whatever global tracer provider is
	// installed (a no-op one unless tracing was initialized); the request
	// must still succeed.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestInitRouter_ShouldSendHstsHeaderWheneverConfiguredRegardlessOfTLS(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := newTestTLSCertPEM(t)

	tests := []struct {
		name      string
		configure func(c *config.Config)
		want      string
	}{
		{
			name: "should send hsts header when inline certificate and hsts value configured",
			configure: func(c *config.Config) {
				c.HTTP.TLS.Certificate = certPEM
				c.HTTP.TLS.PrivateKey = keyPEM
				c.HTTP.Hsts = "max-age=31536000; includeSubDomains"
			},
			want: "max-age=31536000; includeSubDomains",
		},
		{
			name: "should send hsts header when certificate files and hsts value configured",
			configure: func(c *config.Config) {
				// initRouter only checks that a file pair is configured; the
				// files are first read later, in startAppServer.
				c.HTTP.TLS.CertificateFile = "cert.pem"
				c.HTTP.TLS.PrivateKeyFile = "key.pem"
				c.HTTP.Hsts = "max-age=63072000"
			},
			want: "max-age=63072000",
		},
		{
			// A reverse proxy in front may terminate TLS itself, in which
			// case this process only ever sees plain HTTP but the header
			// still needs to reach the browser.
			name: "should send hsts header when tls is not configured but hsts value is",
			configure: func(c *config.Config) {
				c.HTTP.Hsts = "max-age=31536000; includeSubDomains"
			},
			want: "max-age=31536000; includeSubDomains",
		},
		{
			name: "should not send hsts header when hsts value is empty",
			configure: func(c *config.Config) {
				c.HTTP.TLS.Certificate = certPEM
				c.HTTP.TLS.PrivateKey = keyPEM
			},
			want: "",
		},
		{
			name: "should send hsts header when certificate is configured without a key but hsts value is set",
			configure: func(c *config.Config) {
				c.HTTP.TLS.Certificate = certPEM
				c.HTTP.Hsts = "max-age=31536000; includeSubDomains"
			},
			want: "max-age=31536000; includeSubDomains",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &config.Config{}
			tt.configure(c)

			a := newTestApp(t, c)
			srv, err := a.initRouter()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// The health check is registered before every other route, so if
			// its response carries the header, the whole router's do.
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			srv.router.ServeHTTP(w, req)

			if got := w.Header().Get("Strict-Transport-Security"); got != tt.want {
				t.Errorf("got Strict-Transport-Security %q, want %q", got, tt.want)
			}
		})
	}
}

// newTestTLSCertPEM generates a self-signed ECDSA certificate for
// 127.0.0.1, returning the certificate and private key in PEM form.
func newTestTLSCertPEM(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ECDSA key: %v", err)
	}

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ssoosshd-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}

	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

// startServerOnLoopback assigns a fresh loopback listener to s, starts
// s.Run in the background, and returns the listener address plus a channel
// that receives Run's return value. The server is shut down (and Run's
// result verified to be nil) via t.Cleanup.
func startServerOnLoopback(t *testing.T, s *Server) (addr string, runErr chan error) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	s.appListener = ln

	ctx, cancel := context.WithCancel(context.Background())
	runErr = make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("expected Run to return nil after context cancellation, got %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("timed out waiting for Run to return after context cancellation")
		}
	})

	return ln.Addr().String(), runErr
}

// getWithRetry performs GET requests against url with client until one
// succeeds or the deadline passes, returning the last response.
func getWithRetry(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get(url)
		if err == nil {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not respond to %s before deadline: %v", url, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServerRun_ShouldServeHTTPUntilContextCanceled(t *testing.T) {
	t.Parallel()

	a := newTestApp(t, &config.Config{})
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	addr, _ := startServerOnLoopback(t, srv)

	resp := getWithRetry(t, http.DefaultClient, "http://"+addr+"/healthz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if srv.useTLS {
		t.Error("expected useTLS to be false when no certificate is configured")
	}
	// Shutdown and the Run-returns-nil assertion happen in t.Cleanup.
}

func TestServerRun_ShouldServeTLSWhenInlineCertificateConfigured(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := newTestTLSCertPEM(t)

	c := &config.Config{}
	c.HTTP.TLS.Certificate = certPEM
	c.HTTP.TLS.PrivateKey = keyPEM
	c.HTTP.TLS.TLSMinVersion = "TLS1.3" // match the shipped default posture

	a := newTestApp(t, c)
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	addr, _ := startServerOnLoopback(t, srv)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed test certificate
		},
	}
	resp := getWithRetry(t, client, "https://"+addr+"/healthz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if resp.TLS == nil {
		t.Fatal("expected a TLS connection state on the response")
	}
	if resp.TLS.Version != tls.VersionTLS13 {
		t.Errorf("got negotiated TLS version 0x%04x, want TLS 1.3", resp.TLS.Version)
	}
	if !srv.useTLS {
		t.Error("expected useTLS to remain true when an inline certificate is configured")
	}
}

func TestServerRun_ShouldReturnErrorWhenCipherSuitesIncompatibleWithHTTP2(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := newTestTLSCertPEM(t)

	c := &config.Config{}
	c.HTTP.TLS.Certificate = certPEM
	c.HTTP.TLS.PrivateKey = keyPEM
	c.HTTP.TLS.TLSMinVersion = "TLS1.2"
	// An explicit list without an HTTP/2-required AES_128_GCM suite makes
	// ServeTLS fail during net/http's automatic HTTP/2 setup; Run must
	// surface that failure instead of blocking until the context ends.
	c.HTTP.TLS.CipherSuites = []string{"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"}

	a := newTestApp(t, c)
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	srv.appListener = ln

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(context.Background()) }()

	select {
	case err := <-runErr:
		if err == nil {
			t.Fatal("expected Run to return the ServeTLS error, got nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out: Run did not return after ServeTLS failed")
	}
}

func TestServerRun_ShouldReturnErrorWhenListenerAlreadyClosed(t *testing.T) {
	t.Parallel()

	a := newTestApp(t, &config.Config{})
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	ln.Close() // Serve on the closed listener fails immediately
	srv.appListener = ln

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(context.Background()) }()

	select {
	case err := <-runErr:
		if err == nil {
			t.Fatal("expected Run to return the Serve error, got nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out: Run did not return after Serve failed")
	}
}

func TestServerRun_ShouldErrorWhenAlreadyRunning(t *testing.T) {
	t.Parallel()

	a := newTestApp(t, &config.Config{})
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	srv.running.Store(true)
	if err := srv.Run(context.Background()); err == nil {
		t.Fatal("expected an error when the server is already running, got nil")
	}
}

func TestServerRun_ShouldErrorWhenTLSCertificateInvalid(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.HTTP.TLS.Certificate = "not a certificate"
	c.HTTP.TLS.PrivateKey = "not a key"

	a := newTestApp(t, c)
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := srv.Run(context.Background()); err == nil {
		t.Fatal("expected Run to fail when the TLS certificate cannot be parsed, got nil")
	}
	if srv.running.Load() {
		t.Error("expected the running flag to be reset after a failed Run")
	}
}

func TestStartAppServer_ShouldLoadCertificateFromFiles(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := newTestTLSCertPEM(t)
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, []byte(certPEM), 0600); err != nil {
		t.Fatalf("failed to write cert file: %v", err)
	}
	if err := os.WriteFile(keyFile, []byte(keyPEM), 0600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	c := &config.Config{}
	c.HTTP.Address = "127.0.0.1"
	c.HTTP.TLS.CertificateFile = certFile
	c.HTTP.TLS.PrivateKeyFile = keyFile
	c.HTTP.TLS.TLSMinVersion = "TLS1.2"

	a := newTestApp(t, c)
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	srv.appListener = ln

	if err := srv.startAppServer(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	t.Cleanup(func() { _ = srv.appSrv.Close() })

	if !srv.useTLS {
		t.Error("expected useTLS to remain true when certificate files are configured")
	}
	if len(srv.appSrv.TLSConfig.Certificates) != 1 {
		t.Errorf("expected 1 loaded certificate, got %d", len(srv.appSrv.TLSConfig.Certificates))
	}
}

func TestStartAppServer_ShouldErrorWhenTLSSettingsInvalid(t *testing.T) {
	t.Parallel()

	certPEM, keyPEM := newTestTLSCertPEM(t)

	tests := []struct {
		name   string
		mutate func(c *config.Config)
	}{
		{
			name:   "should error when cipher suite name unknown",
			mutate: func(c *config.Config) { c.HTTP.TLS.CipherSuites = []string{"NOT_A_CIPHER_SUITE"} },
		},
		{
			name:   "should error when min version unknown",
			mutate: func(c *config.Config) { c.HTTP.TLS.TLSMinVersion = "TLS9.9" },
		},
		{
			name:   "should error when curve name unknown",
			mutate: func(c *config.Config) { c.HTTP.TLS.CurveNames = []string{"not-a-curve"} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &config.Config{}
			c.HTTP.Address = "127.0.0.1"
			c.HTTP.TLS.Certificate = certPEM
			c.HTTP.TLS.PrivateKey = keyPEM
			c.HTTP.TLS.TLSMinVersion = "TLS1.2"
			tt.mutate(c)

			srv := &Server{router: gin.New(), config: c, useTLS: true}
			if err := srv.startAppServer(context.Background()); err == nil {
				t.Fatal("expected an error for invalid TLS settings, got nil")
			}
		})
	}
}

func TestStartAppServer_ShouldErrorWhenListenerCannotBeCreated(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.HTTP.Address = "256.256.256.256" // not a valid IP, so net.Listen must fail

	srv := &Server{router: gin.New(), config: c, useTLS: true}
	if err := srv.startAppServer(context.Background()); err == nil {
		t.Fatal("expected an error when the TCP listener cannot be created, got nil")
	}
}

// The sdNotifyReady tests set NOTIFY_SOCKET via t.Setenv and therefore must
// not run in parallel.

func TestSdNotifyReady_ShouldBeNoopWhenNotifySocketUnset(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	if err := sdNotifyReady(); err != nil {
		t.Fatalf("expected no error when NOTIFY_SOCKET is unset, got %v", err)
	}
}

func TestSdNotifyReady_ShouldSendReadyToNotifySocket(t *testing.T) {
	// Unix socket paths are limited to ~104 bytes on macOS, which
	// t.TempDir()'s long test-name-derived path exceeds, so create a short
	// temp dir directly under the system temp root instead.
	dir, err := os.MkdirTemp("", "sd")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "sd.sock")
	t.Setenv("NOTIFY_SOCKET", socketPath)

	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		t.Fatalf("failed to listen on unixgram socket: %v", err)
	}
	defer conn.Close()

	if err := sdNotifyReady(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("failed to set read deadline: %v", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("failed to read from notify socket: %v", err)
	}
	if got := string(buf[:n]); got != "READY=1" {
		t.Errorf("got notification %q, want %q", got, "READY=1")
	}
}

func TestSdNotifyReady_ShouldErrorWhenNotifySocketUnreachable(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "does-not-exist.sock"))

	if err := sdNotifyReady(); err == nil {
		t.Fatal("expected an error when the notify socket does not exist, got nil")
	}
}

func TestStartAppServer_ShouldLogWhenServeFailsOnClosedListener(t *testing.T) {
	t.Parallel()

	a := newTestApp(t, &config.Config{})
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}
	srv.appListener = ln

	// startAppServer itself succeeds; the background Serve call then fails
	// immediately on the already-closed listener, which is only logged.
	if err := srv.startAppServer(context.Background()); err != nil {
		t.Fatalf("expected no error from startAppServer, got %v", err)
	}
	t.Cleanup(func() { _ = srv.appSrv.Close() })

	// Give the serve goroutine a moment to observe the closed listener.
	time.Sleep(100 * time.Millisecond)
}
