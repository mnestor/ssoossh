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
// every host — see docs/what-ssoossh-is.md.
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

// checkCASignature is check 1: the certificate must be signed by one of the
// trusted CAs. This is a signature verification (kp.SignedBy), not a string
// comparison against the trusted-CA file's contents — see
// the original design note: "Check 1 is a signature verification,
// not a string comparison".
func checkCASignature(kp *keypair.SSHKeypair, cas []ssh.PublicKey) error {
	for _, ca := range cas {
		if kp.SignedBy(ca) {
			return nil
		}
	}
	return errors.New("certificate is not signed by a trusted CA")
}

// checkKeyBinding is check 2: the certificate's public key must match the
// keypair generated for this attempt. Without this, checks 1, 3, and 4
// passing together would accept any CA-signed certificate carrying the
// right principal, including one issued to somebody else's keypair — see
// the original design note: "Check 2 is the one that would
// otherwise be missing".
func checkKeyBinding(cert *ssh.Certificate, kp *keypair.SSHKeypair) error {
	if cert.Key == nil {
		return errors.New("certificate carries no public key")
	}
	if !bytes.Equal(cert.Key.Marshal(), kp.Public().Marshal()) {
		return errors.New("certificate public key does not match the key generated for this attempt")
	}
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
// behavior instead of locking out every login on the host.
func checkPrincipal(cert *ssh.Certificate, username, mapPath string) error {
	if mapPath != "" {
		if m, err := principalsmap.LoadFromFile(mapPath); err == nil {
			if !m.Allowed(username, cert.ValidPrincipals) {
				return fmt.Errorf("certificate principals %v are not authorized for account %q per %s", cert.ValidPrincipals, username, mapPath)
			}
			return nil
		}
	}

	if !slices.Contains(cert.ValidPrincipals, username) {
		return fmt.Errorf("certificate principals %v do not include %q", cert.ValidPrincipals, username)
	}
	return nil
}

// checkValidityWindow is check 4: now must fall within [ValidAfter,
// ValidBefore], with tolerance applied symmetrically on both bounds to
// absorb clock skew between the server that issued the certificate and this
// host. The returned error names the observed skew, so it can be logged
// verbatim — an operator debugging an intermittent 3am failure needs
// "certificate not yet valid, 4.2s of skew, tolerance 2s", not
// "authentication failed".
func checkValidityWindow(cert *ssh.Certificate, now time.Time, tolerance time.Duration) error {
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
	return nil
}
