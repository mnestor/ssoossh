//go:build e2e

package harness

import (
	"bytes"

	"golang.org/x/crypto/ssh"
)

// ParseAuthorizedKey parses a single public key in authorized_keys format
// (as Server.CAPublicKey is rendered), discarding any trailing comment.
func ParseAuthorizedKey(s string) (ssh.PublicKey, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s)) //nolint:dogsled // the other three returns (comment, options, rest) aren't needed here
	return key, err
}

// SameSSHKey reports whether a and b are the same key, comparing their
// wire encoding rather than pointer identity.
func SameSSHKey(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return false
	}
	return bytes.Equal(a.Marshal(), b.Marshal())
}
