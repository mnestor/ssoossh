package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
)

// runSetup writes into -out what the server container needs before
// ssoosshd starts:
//
//	tls.crt   a self-signed server certificate for -host, localhost and
//	          127.0.0.1
//	tls.key   its private key, mode 0600
//	spki.txt  the certificate's SPKI pin, for nss.yaml's tls_spki_pin
//	ca-key    an ECDSA P-256 SSH CA key for ssoosshd's ssh_key_file, mode 0600
func runSetup(_ context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	dir := fs.String("out", ".", "directory to write the files into")
	host := fs.String("host", "server", "the DNS name hosts reach the server by")
	if err := fs.Parse(args); err != nil {
		return err
	}

	certPEM, keyPEM, pin, err := newServerCertificate(*host, time.Now())
	if err != nil {
		return err
	}
	caKey, err := newCAKey()
	if err != nil {
		return err
	}
	files := []struct {
		name string
		body []byte
		mode os.FileMode
	}{
		{"tls.crt", certPEM, 0o644},
		{"tls.key", keyPEM, 0o600},
		{"spki.txt", []byte(pin + "\n"), 0o644},
		{"ca-key", caKey, 0o600},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(*dir, f.name), f.body, f.mode); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	_, err = fmt.Fprintln(out, pin)
	return err
}

// newServerCertificate makes a self-signed ECDSA certificate for host,
// localhost and 127.0.0.1, valid around now, and returns it and its key
// in PEM with the base64 SHA-256 of its SubjectPublicKeyInfo, the form
// ssoossh-nss pins.
func newServerCertificate(host string, now time.Time) (certPEM, keyPEM []byte, pin string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return nil, nil, "", fmt.Errorf("generate the server key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host, "localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		// not covered: the template above is always valid.
		return nil, nil, "", fmt.Errorf("create the server certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		// not covered: der was just created by the standard library.
		return nil, nil, "", fmt.Errorf("parse the server certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		// not covered: a P-256 key always marshals.
		return nil, nil, "", fmt.Errorf("encode the server key: %w", err)
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		base64.StdEncoding.EncodeToString(sum[:]), nil
}

// newCAKey returns a new ECDSA P-256 SSH private key in OpenSSH PEM
// form. Not Ed25519: the job's ssoosshd is built with the FIPS module, as
// released, and refuses a CA key FIPS 140-3 does not approve.
func newCAKey() ([]byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return nil, fmt.Errorf("generate the CA key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "ssoossh nss e2e CA")
	if err != nil {
		// not covered: a P-256 key always marshals.
		return nil, fmt.Errorf("encode the CA key: %w", err)
	}
	return pem.EncodeToMemory(block), nil
}
