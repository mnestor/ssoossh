package api

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// ErrPinMismatch is a server whose leaf public key matches none of
// Config.TLSSPKIPins. The error that wraps it names the digest the server
// presented, which is what an operator rotating a key needs to see.
var ErrPinMismatch = errors.New("the server's TLS public key matches none of the pinned keys")

// pinned reports whether cfg asks for pinned mode: an explicit trust
// root (a CA file, the system store or key pins), TLS 1.3, no proxy.
func (cfg Config) pinned() bool {
	return cfg.TLSCAFile != "" || cfg.TLSSystemRoots || len(cfg.TLSSPKIPins) > 0
}

// buildTLSConfig assembles the client's TLS settings. Outside pinned mode
// it is the standard verification against the system store (or none,
// with SkipVerifySSL). In pinned mode the configuration names what is
// trusted: see Config.TLSCAFile, Config.TLSSystemRoots and
// Config.TLSSPKIPins.
func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.SkipVerifySSL,
		MinVersion:         tls.VersionTLS13,
	}
	if !cfg.pinned() {
		return tlsConfig, nil
	}

	if cfg.SkipVerifySSL {
		return nil, errors.New("skipping TLS verification cannot be combined with a pinned CA file, the system trust store or SPKI pins")
	}
	if cfg.TLSSystemRoots && cfg.TLSCAFile != "" {
		return nil, errors.New("a TLS CA file and the system trust store are alternatives: set one of them")
	}
	pins, err := parseSPKIPins(cfg.TLSSPKIPins)
	if err != nil {
		return nil, err
	}

	switch {
	case cfg.TLSCAFile != "":
		pool, err := loadCAFile(cfg.TLSCAFile)
		if err != nil {
			return nil, err
		}
		// RootCAs set to a pool of the file's own means the system store
		// is never consulted, and SSL_CERT_FILE and SSL_CERT_DIR, which
		// only feed that store, are ignored with it.
		tlsConfig.RootCAs = pool
	case cfg.TLSSystemRoots:
		pool, err := loadSystemRoots(cfg.systemRoots)
		if err != nil {
			return nil, err
		}
		// The pool is loaded here rather than left nil for crypto/tls to
		// load on the first handshake, so an unreadable or empty store
		// fails NewClient with an error that names the store, not every
		// handshake with one that looks like the server's fault. Go loads
		// the store once per process either way, so neither form sees a
		// change to it before a restart.
		tlsConfig.RootCAs = pool
	default:
		// Pins and no CA: there is no chain to verify, and the system
		// store is exactly what pin-only mode refuses to trust, so the
		// standard check is off and VerifyConnection's pin check below is
		// the whole decision. The handshake still proves the server holds
		// the pinned key's private half.
		tlsConfig.InsecureSkipVerify = true
	}

	if len(pins) > 0 {
		tlsConfig.VerifyConnection = func(cs tls.ConnectionState) error {
			return verifySPKIPins(cs, pins)
		}
	}
	return tlsConfig, nil
}

// parseSPKIPins decodes each pin from base64 and checks it is a SHA-256
// digest. A pin that is wrong is a configuration error now, not a failed
// handshake later that looks like an attack.
func parseSPKIPins(encoded []string) ([][sha256.Size]byte, error) {
	pins := make([][sha256.Size]byte, 0, len(encoded))
	for _, e := range encoded {
		raw, err := base64.StdEncoding.DecodeString(e)
		if err != nil {
			return nil, fmt.Errorf("SPKI pin %q is not base64: %w", e, err)
		}
		if len(raw) != sha256.Size {
			return nil, fmt.Errorf("SPKI pin %q is %d bytes, not a %d-byte SHA-256 digest", e, len(raw), sha256.Size)
		}
		pins = append(pins, [sha256.Size]byte(raw))
	}
	return pins, nil
}

// loadCAFile reads a PEM bundle into a pool of its own. A file with no
// certificate in it is refused: an empty pool would fail every handshake
// with an error that points at the server rather than at the file.
func loadCAFile(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read TLS CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("TLS CA file %s holds no PEM certificate", path)
	}
	return pool, nil
}

// loadSystemRoots loads the system trust store through load, nil meaning
// x509.SystemCertPool. A store with no certificate in it is refused for
// the reason loadCAFile refuses an empty file. On macOS and Windows the
// pool stands for the platform verifier and holds no certificate of its
// own, which Equal tells apart from an empty pool.
func loadSystemRoots(load func() (*x509.CertPool, error)) (*x509.CertPool, error) {
	if load == nil {
		load = x509.SystemCertPool
	}
	pool, err := load()
	if err != nil {
		return nil, fmt.Errorf("load the system trust store: %w", err)
	}
	if pool == nil || pool.Equal(x509.NewCertPool()) {
		return nil, errors.New("the system trust store holds no certificate")
	}
	return pool, nil
}

// verifySPKIPins checks the server's leaf certificate against the pins.
// Called after standard verification, when there is one, so with a CA file
// or the system store a pinned key on a certificate from the wrong CA
// still fails.
func verifySPKIPins(cs tls.ConnectionState, pins [][sha256.Size]byte) error {
	if len(cs.PeerCertificates) == 0 {
		return fmt.Errorf("%w: the server presented no certificate", ErrPinMismatch)
	}
	got := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
	for _, pin := range pins {
		if got == pin {
			return nil
		}
	}
	return fmt.Errorf("%w: it presented %s", ErrPinMismatch, base64.StdEncoding.EncodeToString(got[:]))
}
