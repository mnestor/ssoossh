package tlsutils

import (
	"crypto/tls"
	"errors"
	"fmt"
	"slices"
	"time"
)

// CertificateInfo names a certificate/private-key pair as paths to PEM files
// on disk, for binding into a config struct (e.g. via mapstructure tags) and
// resolving with LoadX509KeyPair.
//
// Paths only: an earlier version also accepted the PEM inline in the config
// file, which was dropped because it cannot be rotated. A file can be
// rewritten by whatever renews it and re-read by CertSource without a
// restart; PEM pasted into the config file can only change when the config
// file does, and the config file is read once at startup.
type CertificateInfo struct {
	// CertificateFile and PrivateKeyFile point to PEM files on disk. Both
	// must be set. Their contents are re-read on reload (see CertSource);
	// the paths themselves are fixed for the process's lifetime.
	CertificateFile string `mapstructure:"certificate_file"`
	PrivateKeyFile  string `mapstructure:"private_key_file"`
}

// HasKeyPair reports whether a complete certificate/private-key pair is
// configured.
func (t CertificateInfo) HasKeyPair() bool {
	return t.CertificateFile != "" && t.PrivateKeyFile != ""
}

// LoadX509KeyPair resolves this CertificateInfo into a usable
// tls.Certificate. Returns an error if no pair is configured or the files
// can't be read or parsed.
//
// tls.LoadX509KeyPair parses both halves and checks that they match before
// returning, so a pair caught half-written fails here rather than being
// served — which is what lets CertSource.Reload treat any failure as
// "keep the previous certificate".
func (t CertificateInfo) LoadX509KeyPair() (tls.Certificate, error) {
	if !t.HasKeyPair() {
		return tls.Certificate{}, errors.New("no certificate/private key pair configured")
	}

	cert, err := tls.LoadX509KeyPair(t.CertificateFile, t.PrivateKeyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("loading certificate_file/private_key_file: %w", err)
	}

	return cert, nil
}

// TLSConfig configures TLS for an HTTP(S) server.
//
// The certificate and key are given as paths to PEM files
// (CertificateFile/PrivateKeyFile); both must be set. If the pair is
// incomplete, callers typically run without TLS instead.
//
// CipherSuites, TLSMinVersion, and CurveNames are resolved by this
// package's CipherSuites, MinVersion, and Curve functions: an unrecognized
// name fails, and a configuration that crypto/tls or net/http rejects (see
// CipherSuites) should shut the server down rather than be logged and
// ignored.
type TLSConfig struct {
	CertificateInfo

	// CipherSuites restricts the TLS 1.0-1.2 cipher suites offered (Go
	// does not allow configuring TLS 1.3 suites). Names must exactly match
	// crypto/tls constant names, case-sensitively, e.g.
	// "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"; the stdlib's insecure
	// suites resolve too. Empty means Go's defaults. Any explicit list
	// must include TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256 or
	// TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256: net/http's automatic HTTP/2
	// setup refuses to serve without one of them, failing the server at
	// startup.
	CipherSuites []string `mapstructure:"cipher_suites"`

	// TLSMinVersion is the minimum TLS protocol version: TLS1.0, TLS1.1,
	// TLS1.2, or TLS1.3. Parsing tolerates case, "."/"-"/"_"/" "
	// separators, and the "tls"/"version" prefixes ("tls12", "TLS-1.2",
	// and "VersionTLS12" all work). TLS1.0 and TLS1.1 resolve but log a
	// deprecation warning (RFC 8996).
	TLSMinVersion string `mapstructure:"min_version"`

	// CurveNames restricts the key-exchange curves/groups offered, in
	// preference order: P256, P384, P521, X25519, or X25519MLKEM768, with
	// the same tolerant parsing as TLSMinVersion ("CurveP256", "p-256",
	// ...). Empty means Go's defaults; note that naming any curve replaces
	// those defaults entirely rather than adding to them.
	CurveNames []string `mapstructure:"curves"`

	// ReloadInterval is how often the certificate files are re-read on a
	// timer, in addition to SIGHUP which always reloads. Zero or negative
	// disables the timer, leaving SIGHUP as the only trigger.
	//
	// The timer exists for deployments where nothing signals the process:
	// a Kubernetes secret remount is the motivating case, since it replaces
	// a directory symlink rather than writing the files in place, so a
	// filesystem watch on the paths never fires.
	ReloadInterval time.Duration `mapstructure:"reload_interval"`
}

// fipsApprovedCipherSuites are the AES-GCM suites FIPS 140-3 approves:
// ChaCha20-Poly1305 and the CBC-mode suites aren't included, deliberately
// narrower than the "secure" set CipherSuites otherwise accepts. Includes
// the TLS 1.3 AES-GCM suites too, even though Go doesn't let CipherSuites
// configure TLS 1.3 negotiation (see TLSConfig.CipherSuites) -- listing
// them keeps an operator who names one explicitly from hitting a spurious
// FIPS rejection.
//
// This list intentionally includes TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
// and TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, satisfying net/http's HTTP/2
// requirement (see TLSConfig.CipherSuites) so defaulting to this set under
// FIPS never breaks HTTP/2.
var fipsApprovedCipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_AES_128_GCM_SHA256,
	tls.TLS_AES_256_GCM_SHA384,
}

// fipsApprovedCurves are the NIST curves FIPS 140-3 approves for TLS key
// exchange. X25519 and X25519MLKEM768 aren't included.
var fipsApprovedCurves = []tls.CurveID{tls.CurveP256, tls.CurveP384, tls.CurveP521}

// requireFIPSApprovedCipherSuites errors on the first id not in
// fipsApprovedCipherSuites, failing closed the same way an unrecognized
// name does in CipherSuites.
func requireFIPSApprovedCipherSuites(ids []uint16) error {
	for _, id := range ids {
		if !slices.Contains(fipsApprovedCipherSuites, id) {
			return fmt.Errorf("tlsconfig: cipher suite %q is not FIPS-approved", tls.CipherSuiteName(id))
		}
	}
	return nil
}

// requireFIPSApprovedCurves errors on the first id not in
// fipsApprovedCurves, failing closed the same way an unrecognized name does
// in Curve.
func requireFIPSApprovedCurves(ids []tls.CurveID) error {
	for _, id := range ids {
		if !slices.Contains(fipsApprovedCurves, id) {
			return fmt.Errorf("tlsconfig: curve %q is not FIPS-approved", id)
		}
	}
	return nil
}

// Build resolves this TLSConfig into a usable *tls.Config and the CertSource
// backing it: it loads the certificate/key pair and resolves CipherSuites,
// TLSMinVersion, and CurveNames via this package's CipherSuites, MinVersion,
// and Curve functions. If no certificate/key pair is configured, Build
// returns (nil, nil, nil) rather than an error -- callers use that nil to
// mean "TLS doesn't apply here" (see bootstrap.configureAppServerTransport).
// Build still returns an error if a certificate/key pair is configured but
// unloadable, or if any of the three fields above name something
// unrecognized.
//
// The returned config resolves its certificate through GetCertificate rather
// than a pinned Certificates list, so the caller can hand the CertSource to a
// reload trigger and have renewals reach live listeners. See CertSource.
//
// When fipsEnabled is true, an explicitly configured CipherSuites or
// CurveNames must resolve entirely within the FIPS-approved sets above
// (fail closed on the first one that doesn't); left empty, they default to
// those sets instead of Go's own defaults.
//
// tls.Config.ServerName is deliberately left unset: it's a client-side
// field servers ignore, so enforcing a specific server name is a caller
// concern, not this package's.
func (t TLSConfig) Build(fipsEnabled bool) (*tls.Config, *CertSource, error) {
	if !t.HasKeyPair() {
		return nil, nil, nil
	}

	source, err := NewCertSource(t.CertificateInfo)
	if err != nil {
		return nil, nil, err
	}

	cipherSuites, err := CipherSuites(t.CipherSuites)
	if err != nil {
		return nil, nil, err
	}
	minVersion, err := MinVersion(t.TLSMinVersion)
	if err != nil {
		return nil, nil, err
	}
	curves, err := Curve(t.CurveNames)
	if err != nil {
		return nil, nil, err
	}

	if fipsEnabled {
		if cipherSuites == nil {
			cipherSuites = fipsApprovedCipherSuites
		} else if err := requireFIPSApprovedCipherSuites(cipherSuites); err != nil {
			return nil, nil, err
		}
		if curves == nil {
			curves = fipsApprovedCurves
		} else if err := requireFIPSApprovedCurves(curves); err != nil {
			return nil, nil, err
		}
	}

	return &tls.Config{
		GetCertificate:   source.GetCertificate,
		MinVersion:       minVersion,
		CurvePreferences: curves,
		CipherSuites:     cipherSuites,
	}, source, nil
}
