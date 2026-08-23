package agent

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// parseCAPublicKey parses a CA public key from an authorized_keys-format
// string, falling back to raw base64-encoded key bytes.
func parseCAPublicKey(caStr string) (ssh.PublicKey, error) {
	caStr = strings.TrimSpace(caStr)
	if caStr == "" {
		return nil, errors.New("CA public key string cannot be empty")
	}
	pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(caStr))
	if err == nil && len(rest) == 0 {
		return pub, nil
	}
	data, err := base64.StdEncoding.DecodeString(caStr)
	if err != nil {
		return nil, errors.New("failed to parse CA public key string")
	}
	pub, err = ssh.ParsePublicKey(data)
	if err != nil {
		return nil, errors.New("failed to parse CA public key from base64")
	}
	return pub, nil
}

// parseCAPublicKeys parses each of cas via parseCAPublicKey, used by both
// SshAgent.SetCA and FileAgent.SetCA. Each string may itself hold several
// keys, one per line — the shape the server's /api/ca endpoint returns when
// multiple signer keys are active — so every string is split on newlines and
// each non-blank line parsed on its own (authorized_keys format or raw
// base64). A string with no keys on any line is an error, not silently empty.
func parseCAPublicKeys(cas []string) ([]ssh.PublicKey, error) {
	parsed := make([]ssh.PublicKey, 0, len(cas))
	for _, caStr := range cas {
		found := false
		for _, line := range strings.Split(caStr, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			pub, err := parseCAPublicKey(line)
			if err != nil {
				return nil, err
			}
			parsed = append(parsed, pub)
			found = true
		}
		if !found {
			return nil, errors.New("CA public key string cannot be empty")
		}
	}
	return parsed, nil
}

// publicKeysEqual compares two ssh.PublicKey values for equality.
func publicKeysEqual(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return false
	}
	return bytes.Equal(a.Marshal(), b.Marshal())
}

// CertificateValid reports whether c is time-valid and cryptographically
// signed by any of the given trusted CAs.
func CertificateValid(c *ssh.Certificate, cas []ssh.PublicKey) bool {
	if c == nil {
		return false
	}
	if c.ValidBefore == 0 || c.ValidAfter == 0 {
		return false
	}
	if c.ValidBefore < c.ValidAfter {
		return false
	}
	if c.ValidBefore < uint64(time.Now().Unix()) { //nolint:gosec // time.Now().Unix() is always positive for any real-world date, no overflow risk
		return false
	}
	if c.SignatureKey == nil {
		return false
	}
	for _, ca := range cas {
		if publicKeysEqual(c.SignatureKey, ca) && keypair.VerifyCertSignature(c, ca) {
			return true
		}
	}
	return false
}
