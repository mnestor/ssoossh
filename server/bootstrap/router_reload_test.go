package bootstrap

// Test methodology: these drive a real listener and a real TLS handshake
// rather than asserting on CertSource in isolation — the behavior under test
// is "a certificate rewritten on disk reaches connections accepted after it",
// and only an end-to-end handshake shows that. tlsutils' own tests cover the
// swap semantics; these cover the wiring and the triggers.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
)

// newNamedTLSCertPEM generates a self-signed certificate for 127.0.0.1 whose
// common name is commonName, so a test can tell which certificate a
// handshake actually returned. newTestTLSCertPEM's fixed name cannot.
func newNamedTLSCertPEM(t *testing.T, commonName string) (certPEM, keyPEM string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// writeCertPair writes a named self-signed pair to the fixed paths under dir.
func writeCertPair(t *testing.T, dir, commonName string) (certFile, keyFile string) {
	t.Helper()

	certPEM, keyPEM := newNamedTLSCertPEM(t, commonName)
	certFile = filepath.Join(dir, "server.crt")
	keyFile = filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, []byte(certPEM), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, []byte(keyPEM), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	return certFile, keyFile
}

// tlsServerOnLoopback starts a TLS server whose certificate lives in a fresh
// temp dir, and returns the dir and the address to dial.
func tlsServerOnLoopback(t *testing.T, commonName string, reloadInterval time.Duration) (dir, addr string) {
	t.Helper()

	dir = t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, commonName)

	c := &config.Config{}
	c.HTTP.Address = "127.0.0.1"
	c.HTTP.TLS.CertificateFile = certFile
	c.HTTP.TLS.PrivateKeyFile = keyFile
	c.HTTP.TLS.TLSMinVersion = "TLS1.3"
	c.HTTP.TLS.ReloadInterval = reloadInterval

	a := newTestApp(t, c)
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}

	addr, _ = startServerOnLoopback(t, srv)

	return dir, addr
}

// tryHandshakeCommonName dials addr and returns the common name of the
// certificate the server presented, or an error. Startup is asynchronous
// (Run is launched in a goroutine), so an early dial can legitimately fail.
func tryHandshakeCommonName(addr string) (string, error) {
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13})
	if err != nil {
		return "", err
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("server presented no certificate")
	}

	return certs[0].Subject.CommonName, nil
}

// handshakeCommonName is tryHandshakeCommonName for the point in a test
// where the server is known to be serving and a failure is a real failure.
func handshakeCommonName(t *testing.T, addr string) string {
	t.Helper()

	name, err := tryHandshakeCommonName(addr)
	if err != nil {
		t.Fatalf("handshake with %s: %v", addr, err)
	}

	return name
}

// waitForCommonName polls the server until it presents want, or fails the
// test. It tolerates dial errors so it doubles as "wait until serving",
// which every test here needs before touching the certificate files: the
// initial load happens on Run's goroutine, and overwriting the files before
// it completes would fail startup instead of exercising a reload.
func waitForCommonName(t *testing.T, addr, want string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var (
		got     string
		lastErr error
	)
	for time.Now().Before(deadline) {
		if got, lastErr = tryHandshakeCommonName(addr); lastErr == nil && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Errorf("server presents %q (last error %v), want %q", got, lastErr, want)
}

func TestServerRun_ShouldPresentTheReplacementCertificateAfterSIGHUP(t *testing.T) {
	// Deliberately not parallel: this signals its own process, so every
	// other running server reloads too. That is harmless (their files have
	// not changed, so they reload the same certificate), but serializing
	// keeps the failure mode obvious if it ever stops being harmless.
	dir, addr := tlsServerOnLoopback(t, "before-sighup", 0)

	// Startup runs on Run's goroutine; rewriting the files before its
	// initial load completes would fail startup rather than test a reload.
	waitForCommonName(t, addr, "before-sighup")

	writeCertPair(t, dir, "after-sighup")
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}

	waitForCommonName(t, addr, "after-sighup")
}

func TestServerRun_ShouldPresentTheReplacementCertificateAfterTheReloadInterval(t *testing.T) {
	t.Parallel()

	// The interval trigger is what covers a Kubernetes secret remount,
	// where nothing signals the process and the mount is a symlink swap
	// that a filesystem watch would not see.
	dir, addr := tlsServerOnLoopback(t, "before-tick", 20*time.Millisecond)

	waitForCommonName(t, addr, "before-tick")

	writeCertPair(t, dir, "after-tick")

	waitForCommonName(t, addr, "after-tick")
}

func TestServerRun_ShouldKeepServingThePreviousCertificateWhenAReloadFails(t *testing.T) {
	t.Parallel()

	dir, addr := tlsServerOnLoopback(t, "survivor", 20*time.Millisecond)

	waitForCommonName(t, addr, "survivor")

	// A certificate replaced while its key has not been written yet: the
	// pair no longer matches, which is exactly the transient state a reload
	// must ride out rather than fail the listener on.
	certPEM, _ := newNamedTLSCertPEM(t, "half-written")
	if err := os.WriteFile(filepath.Join(dir, "server.crt"), []byte(certPEM), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}

	// Long enough for several reload attempts to have been made and failed.
	time.Sleep(200 * time.Millisecond)

	if got := handshakeCommonName(t, addr); got != "survivor" {
		t.Errorf("got common name %q, want the previous certificate %q", got, "survivor")
	}
}

func TestStartAppServer_ShouldNotBuildACertSourceWithoutTLS(t *testing.T) {
	t.Parallel()

	// No certificate configured means no CertSource, which is what Run
	// checks before starting the reload watcher: behind a TLS-terminating
	// proxy there is nothing here to reload.
	//
	// startAppServer is called directly rather than through Run so the
	// assertion reads certSource on the same goroutine that wrote it.
	c := &config.Config{}
	c.HTTP.Address = "127.0.0.1"

	a := newTestApp(t, c)
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv.appListener = ln

	if err := srv.startAppServer(t.Context()); err != nil {
		t.Fatalf("startAppServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.appSrv.Close() })

	if srv.certSource != nil {
		t.Error("expected no CertSource when no certificate is configured")
	}
}
