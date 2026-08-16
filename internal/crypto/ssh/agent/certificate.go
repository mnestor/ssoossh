package agent

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
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

// CertificateValid reports whether c is time-valid and signed by any of the
// given trusted CAs.
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
		if publicKeysEqual(c.SignatureKey, ca) {
			return true
		}
	}
	return false
}
