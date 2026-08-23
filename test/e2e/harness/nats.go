//go:build e2e || resilience || load

package harness

// NATS bring-up for the split-mode tier: a real nats-server in Docker with
// the same mTLS posture production requires, PKI generated per run. The
// networking mirrors server/pubsub's integration test - inside a
// devcontainer talking to the host docker daemon the broker joins this
// process's own network namespace, since published ports bind on the host's
// loopback and the default bridge is a different network; on a bare host it
// publishes a port normally. Certs travel in via docker cp because the
// daemon may not share this container's filesystem.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// NATS is a running broker plus the client credentials that reach it.
type NATS struct {
	// URL is the loopback connection string.
	URL string
	// CertFile/KeyFile/CAFile are the client-side mTLS material, in the
	// shape config.NATSConfig wants.
	CertFile, KeyFile, CAFile string
}

// PubSubYAML renders the top-level pubsub config section pointing at this
// broker, for ServerOptions.ExtraConfigYAML.
func (n *NATS) PubSubYAML() string {
	return fmt.Sprintf(
		"pubsub:\n  backend: nats\n  nats:\n    url: %q\n    cert_file: %q\n    key_file: %q\n    ca_file: %q\n",
		n.URL, n.CertFile, n.KeyFile, n.CAFile)
}

// StartNATS generates a throwaway PKI, starts a TLS-verifying nats-server
// container on port, and blocks until a real mTLS connect would succeed
// (verified by the server accepting the TLS handshake; the app-level
// connect is the caller's own code path). Skips only when the docker
// daemon itself is absent, per the postgres harness convention.
func StartNATS(t *testing.T, port int) *NATS {
	t.Helper()

	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("harness: docker daemon unavailable: %v", err)
	}

	dir := t.TempDir()
	writeNATSTestPKI(t, dir)

	args := []string{"create"}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		self, err := os.Hostname()
		if err != nil {
			t.Fatalf("harness: hostname: %v", err)
		}
		args = append(args, "--network", "container:"+self)
	} else {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, port))
	}
	args = append(args, "nats:2-alpine",
		"--port", fmt.Sprintf("%d", port),
		"--tls",
		"--tlscert", "/certs/server.pem",
		"--tlskey", "/certs/server-key.pem",
		"--tlsverify",
		"--tlscacert", "/certs/ca.pem",
	)

	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("harness: docker create nats: %v\n%s", err, out)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() }) //nolint:errcheck // best-effort teardown.

	if out, err := exec.Command("docker", "cp", dir+"/.", id+":/certs").CombinedOutput(); err != nil {
		t.Fatalf("harness: docker cp nats certs: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "start", id).CombinedOutput(); err != nil {
		t.Fatalf("harness: docker start nats: %v\n%s", err, out)
	}

	n := &NATS{
		URL:      fmt.Sprintf("nats://127.0.0.1:%d", port),
		CertFile: filepath.Join(dir, "client.pem"),
		KeyFile:  filepath.Join(dir, "client-key.pem"),
		CAFile:   filepath.Join(dir, "ca.pem"),
	}

	// Readiness: poll until the TCP port accepts. The TLS handshake and
	// queue semantics are then exercised by the processes under test, which
	// retry their own connects on startup.
	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", strings.TrimPrefix(n.URL, "nats://"), time.Second)
		if err == nil {
			_ = conn.Close()
			return n
		}
		if time.Now().After(deadline) {
			t.Fatalf("harness: nats-server never accepted at %s: %v", n.URL, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// writeNATSTestPKI writes ca.pem, server.pem/server-key.pem (SAN
// 127.0.0.1/localhost) and client.pem/client-key.pem into dir.
func writeNATSTestPKI(t *testing.T, dir string) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("harness: generate CA key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ssoossh-e2e-nats-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(2 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("harness: create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("harness: parse CA cert: %v", err)
	}

	write := func(name string, block *pem.Block) {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatalf("harness: write %s: %v", name, err)
		}
	}
	write("ca.pem", &pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leaf := func(cn string, serial int64, server bool) (certName, keyName string) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("harness: generate %s key: %v", cn, err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(2 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
		}
		if server {
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
			tmpl.DNSNames = []string{"localhost"}
			certName, keyName = "server.pem", "server-key.pem"
		} else {
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			certName, keyName = "client.pem", "client-key.pem"
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatalf("harness: create %s cert: %v", cn, err)
		}
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatalf("harness: marshal %s key: %v", cn, err)
		}
		write(certName, &pem.Block{Type: "CERTIFICATE", Bytes: der})
		write(keyName, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		return certName, keyName
	}
	leaf("nats-server", 2, true)
	leaf("ssoossh-e2e-node", 3, false)
}

// StartSigner runs `ssoosshd sign` against configPath and registers
// teardown. Readiness is the signer's DEBUG log announcing its sign-queue
// subscription: core NATS delivers nothing published before a subscriber
// exists, so a test that approves before the signer is listening would
// hang on a lost job and fail for the wrong reason.
func StartSigner(t *testing.T, ssoosshdPath, configPath string) {
	t.Helper()

	logPath := filepath.Join(t.TempDir(), "signer.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("harness: create signer log: %v", err)
	}

	cmd := exec.Command(ssoosshdPath, "sign", "--config", configPath)
	// A file rather than an in-memory buffer: the poll below reads while
	// the process writes, and bytes.Buffer/strings.Builder are not safe
	// for that. Reading the file back also survives the process dying.
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: failed to start signer: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = logFile.Close()
	})

	// The marker is the signer's handler registration/run reaching the
	// sign queue; "certrequest" covers both the handler name
	// (certrequest-signer) and the topic (certrequest.sign) so a log
	// wording change does not silently break readiness.
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, _ := os.ReadFile(logPath)
		if strings.Contains(string(out), "certrequest") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("harness: signer never subscribed to the sign queue; output:\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
