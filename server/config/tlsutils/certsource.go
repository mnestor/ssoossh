package tlsutils

import (
	"crypto/tls"
	"errors"
	"fmt"
	"sync/atomic"
)

// CertSource holds the certificate the server is currently serving and
// re-reads it from disk on demand.
//
// crypto/tls resolves tls.Config.Certificates once, when the config is built,
// which pins the certificate for the process's lifetime. Renewal makes that
// wrong: an operator (or certbot, cert-manager, a Vault agent) rewrites the
// PEM files and expects the running server to pick them up. Routing the
// lookup through GetCertificate instead means the certificate is read once
// per handshake, so a reload reaches every connection accepted after it
// without restarting the listener or dropping the socket.
//
// The pointer is swapped atomically because GetCertificate runs on every
// inbound handshake, concurrently with whatever goroutine calls Reload.
type CertSource struct {
	// info names the PEM files to re-read. It is fixed at construction:
	// which files to read is configuration, and configuration is not
	// reloadable (see model.ServerSecret). Only their contents are.
	info CertificateInfo

	current atomic.Pointer[tls.Certificate]
}

// NewCertSource loads the pair named by info and returns a source serving it.
// It fails if the pair cannot be loaded, so a misconfigured certificate stops
// the server at startup rather than at the first handshake.
func NewCertSource(info CertificateInfo) (*CertSource, error) {
	source := &CertSource{info: info}
	if err := source.Reload(); err != nil {
		return nil, err
	}

	return source, nil
}

// GetCertificate satisfies tls.Config.GetCertificate. The ClientHelloInfo is
// ignored: this server answers for one name, so there is nothing to select
// on.
func (s *CertSource) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := s.current.Load()
	if cert == nil {
		return nil, errors.New("tlsutils: no certificate has been loaded")
	}

	return cert, nil
}

// Reload re-reads the configured PEM files and swaps the certificate in on
// success.
//
// A pair that fails to load leaves the previous certificate in place and
// returns the error. That is deliberate: the common reasons for failure are
// transient states of a file another process is in the middle of rewriting —
// a certificate written before its key, a truncated file, a file briefly
// absent — and none of them is a reason to stop serving TLS with the
// certificate already in hand. LoadX509KeyPair parses the pair before
// returning, so a half-written pair is rejected here rather than served.
func (s *CertSource) Reload() error {
	cert, err := s.info.LoadX509KeyPair()
	if err != nil {
		return fmt.Errorf("reloading certificate: %w", err)
	}

	s.current.Store(&cert)

	return nil
}
