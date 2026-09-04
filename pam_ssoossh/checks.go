//go:build pam

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"github.com/mnestor/ssoossh/internal/principalsmap"
)

// parseTrustedCAs reads path and parses it as authorized_keys format, one CA
// per line, so a deployment can rotate CAs without a coordinated restart of
// every host — see docs/guide/features.md.
func parseTrustedCAs(path string) ([]ssh.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read trusted CA file: %w", err)
	}

	var cas []ssh.PublicKey
	for len(data) > 0 {
		pub, _, _, rest, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse trusted CA file: %w", err)
		}
		cas = append(cas, pub)
		data = rest
	}
	if len(cas) == 0 {
		return nil, errors.New("trusted CA file contains no keys")
	}
	return cas, nil
}

// fingerprint renders a key as its SHA256 fingerprint for log lines, or
// "<none>" when there is no key, so a certificate with no SignatureKey still
// produces a readable message instead of a nil dereference.
func fingerprint(k ssh.PublicKey) string {
	if k == nil {
		return "<none>"
	}
	return ssh.FingerprintSHA256(k)
}

// fingerprints renders every key in keys the way fingerprint does.
func fingerprints(keys []ssh.PublicKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fingerprint(k))
	}
	return out
}

// Every check below logs one line at debug on success saying what it
// compared, so an operator running with `debug` can follow the decision
// from the log alone. Failures need no debug line: the returned error
// carries the same detail and run (pam_ssoossh.go) logs it at error level
// unconditionally.

// checkCASignature is check 1: the certificate must be signed by one of the
// trusted CAs. This is a signature verification (kp.SignedBy), not a string
// comparison against the trusted-CA file's contents — see
// the original design note: "Check 1 is a signature verification,
// not a string comparison".
func checkCASignature(log Logger, kp *keypair.SSHKeypair, cas []ssh.PublicKey) error {
	for i, ca := range cas {
		if kp.SignedBy(ca) {
			log.Debugf("check 1/4 CA signature: signed by %s, trusted CA %d of %d", fingerprint(ca), i+1, len(cas))
			return nil
		}
	}
	var claimed ssh.PublicKey
	if cert := kp.Certificate(); cert != nil {
		claimed = cert.SignatureKey
	}
	return fmt.Errorf("certificate is not signed by a trusted CA (signature key %s, trusted %v)", fingerprint(claimed), fingerprints(cas))
}

// checkKeyBinding is check 2: the certificate's public key must match the
// keypair generated for this attempt. Without this, checks 1, 3, and 4
// passing together would accept any CA-signed certificate carrying the
// right principal, including one issued to somebody else's keypair — see
// the original design note: "Check 2 is the one that would
// otherwise be missing".
func checkKeyBinding(log Logger, cert *ssh.Certificate, kp *keypair.SSHKeypair) error {
	if cert.Key == nil {
		return errors.New("certificate carries no public key")
	}
	if !bytes.Equal(cert.Key.Marshal(), kp.Public().Marshal()) {
		return fmt.Errorf("certificate public key %s does not match the key generated for this attempt %s", fingerprint(cert.Key), fingerprint(kp.Public()))
	}
	log.Debugf("check 2/4 key binding: certificate key %s matches the key generated for this attempt", fingerprint(cert.Key))
	return nil
}

// checkPrincipal is check 3: the certificate's principals must authorize the
// local account PAM is authenticating (the value GetUser returned), not an
// OIDC identity the module never sees.
//
// With no principals-map configured (mapPath == ""), this is an exact
// match: cert.ValidPrincipals must literally contain username. With one
// configured, a certificate principal mapped to username in the file is
// also accepted. A map that is configured but fails to load (missing file,
// malformed YAML) falls back to the exact-match check rather than failing
// the login — a typo'd path or a corrupted file degrades to today's
// behavior instead of locking out every login on the host. That fallback
// is logged at warning level, not debug: an operator who configured a map
// needs to learn it is being ignored without first turning debug on.
func checkPrincipal(log Logger, cert *ssh.Certificate, username, mapPath string) error {
	if mapPath != "" {
		m, err := principalsmap.LoadFromFile(mapPath)
		if err == nil {
			allowed := m[username]
			if !m.Allowed(username, cert.ValidPrincipals) {
				return fmt.Errorf("certificate principals %v are not authorized for account %q per %s (allowed: %v)", cert.ValidPrincipals, username, mapPath, allowed)
			}
			matched := firstCommon(cert.ValidPrincipals, allowed)
			log.Debugf("check 3/4 principal: %q authorized for account %q via principals-map %s (allowed %v, certificate principals %v)", matched, username, mapPath, allowed, cert.ValidPrincipals)
			return nil
		}
		log.Warningf("principals-map %s could not be loaded, falling back to exact principal match: %v", mapPath, err)
	}

	if !slices.Contains(cert.ValidPrincipals, username) {
		return fmt.Errorf("certificate principals %v do not include %q", cert.ValidPrincipals, username)
	}
	log.Debugf("check 3/4 principal: certificate principals %v include account %q (exact match)", cert.ValidPrincipals, username)
	return nil
}

// firstCommon returns the first element of a that also appears in b, or ""
// when there is none. It names the principal a map decision matched on, for
// the debug line; the decision itself is PrincipalsMap.Allowed's.
func firstCommon(a, b []string) string {
	for _, p := range a {
		if slices.Contains(b, p) {
			return p
		}
	}
	return ""
}

// checkValidityWindow is check 4: now must fall within [ValidAfter,
// ValidBefore], with tolerance applied symmetrically on both bounds to
// absorb clock skew between the server that issued the certificate and this
// host. The returned error names the observed skew, so it can be logged
// verbatim — an operator debugging an intermittent 3am failure needs
// "certificate not yet valid, 4.2s of skew, tolerance 2s", not
// "authentication failed".
func checkValidityWindow(log Logger, cert *ssh.Certificate, now time.Time, tolerance time.Duration) error {
	validAfter := time.Unix(int64(cert.ValidAfter), 0)   //nolint:gosec // a certificate's ValidAfter is a Unix timestamp set by the server
	validBefore := time.Unix(int64(cert.ValidBefore), 0) //nolint:gosec // a certificate's ValidBefore is a Unix timestamp set by the server

	if now.Before(validAfter.Add(-tolerance)) {
		skew := validAfter.Sub(now)
		return fmt.Errorf("certificate not yet valid, %s of skew, tolerance %s", skew, tolerance)
	}
	if now.After(validBefore.Add(tolerance)) {
		skew := now.Sub(validBefore)
		return fmt.Errorf("certificate expired, %s of skew, tolerance %s", skew, tolerance)
	}
	log.Debugf("check 4/4 validity window: now %s is within [%s, %s] (tolerance %s, %s remaining)",
		now.UTC().Format(time.RFC3339), validAfter.UTC().Format(time.RFC3339), validBefore.UTC().Format(time.RFC3339),
		tolerance, validBefore.Sub(now).Truncate(time.Second))
	return nil
}
