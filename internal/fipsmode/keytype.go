// Package fipsmode centralizes FIPS 140-3 policy shared by client, server,
// and pam_ssoossh: which SSH key algorithms are FIPS-approved, what to
// default to, and whether FIPS mode itself is in effect. It exists because
// client, server, and pam_ssoossh must not import each other directly, but
// all three need this logic.
package fipsmode

import "slices"

// SSHKeyType names a key algorithm as written in configuration, e.g.
// `type: ed25519`.
type SSHKeyType string

const (
	SSHKeyTypeEd25519 SSHKeyType = "ed25519"
	SSHKeyTypeECDSA   SSHKeyType = "ecdsa"
	SSHKeyTypeRSA     SSHKeyType = "rsa"
)

// Default sizes per algorithm, used when a size isn't otherwise configured.
const (
	// defaultECDSASize is P-384: the unconditional default key type/size
	// across client and PAM.
	defaultECDSASize = 384

	// defaultRSASize follows NIST's recommendation of at least 3072 bits
	// for security beyond 2030.
	defaultRSASize = 3072

	// minRSASize is the smallest modulus keypair.NewRSAKeyPair accepts.
	minRSASize = 2048
)

// validECDSASizes are the NIST curves keypair.NewECDSAKeyPair understands.
var validECDSASizes = []int{256, 384, 521}

// fipsApprovedKeyTypes are the algorithms FIPS-mode SSH implementations
// generally accept.
//
// ed25519 is deliberately absent. It only entered FIPS 186-5 in 2023, and
// several FIPS policies still reject ssh-ed25519 outright — so a key
// generated with it may simply be unusable against a FIPS-mode server,
// even though the algorithm itself is excellent.
var fipsApprovedKeyTypes = []SSHKeyType{SSHKeyTypeECDSA, SSHKeyTypeRSA}

// DefaultSSHKeyType is the unconditional default SSH key type: ECDSA
// P-384, FIPS-approved and now the default whether or not FIPS is active.
func DefaultSSHKeyType() SSHKeyType {
	return SSHKeyTypeECDSA
}

// DefaultSizeForAlgorithm returns the default key size for t. It returns 0
// for ed25519, which has exactly one size, and for any unrecognized type.
func DefaultSizeForAlgorithm(t SSHKeyType) int {
	switch t {
	case SSHKeyTypeECDSA:
		return defaultECDSASize
	case SSHKeyTypeRSA:
		return defaultRSASize
	default:
		return 0
	}
}

// IsApprovedInFIPS reports whether t is an algorithm FIPS-mode SSH
// implementations generally accept.
func IsApprovedInFIPS(t SSHKeyType) bool {
	return slices.Contains(fipsApprovedKeyTypes, t)
}

// ValidECDSASizes are the NIST curves keypair.NewECDSAKeyPair understands.
func ValidECDSASizes() []int {
	return validECDSASizes
}

// MinRSASize is the smallest RSA modulus keypair.NewRSAKeyPair accepts.
func MinRSASize() int {
	return minRSASize
}

// FromSSHAlgorithm maps an ssh.PublicKey.Type() string to an SSHKeyType,
// with ok=false for anything unrecognized. This is the single source of
// truth for the algorithm-name mapping, shared by every FIPS check in the
// codebase so it can't drift between them.
func FromSSHAlgorithm(algo string) (t SSHKeyType, ok bool) {
	switch algo {
	case "ssh-ed25519":
		return SSHKeyTypeEd25519, true
	case "ssh-rsa":
		return SSHKeyTypeRSA, true
	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		return SSHKeyTypeECDSA, true
	default:
		return "", false
	}
}
