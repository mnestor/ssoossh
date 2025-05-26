package agent

import (
	"time"

	"golang.org/x/crypto/ssh"
)

func CertificateValid(c *ssh.Certificate, ca ssh.PublicKey) bool {
	if c == nil {
		return false
	}
	if c.ValidBefore == 0 || c.ValidAfter == 0 {
		return false
	}
	if c.ValidBefore < c.ValidAfter {
		return false
	}
	if c.ValidBefore < uint64(time.Now().Unix()) {
		return false
	}
	if c.SignatureKey == nil || !publicKeysEqual(c.SignatureKey, ca) {
		return false
	}
	return true
}
