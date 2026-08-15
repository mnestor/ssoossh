package config

import (
	"crypto/fips140"
	"fmt"
	"slices"
)

// Default sizes per algorithm, used when SSHKeyOptions.Size is unset.
const (
	// defaultECDSASize is P-256: FIPS-approved, supported by OpenSSH since
	// 5.7, and by far the cheapest of the approved options to generate —
	// which matters because a fresh keypair is generated for every
	// certificate.
	defaultECDSASize = 256

	// defaultRSASize follows NIST's recommendation of at least 3072 bits
	// for security beyond 2030. Note RSA generation is orders of magnitude
	// slower than the alternatives (hundreds of milliseconds, and highly
	// variable), paid on every login.
	defaultRSASize = 3072

	// minRSASize is the smallest modulus keypair.NewRSAKeyPair accepts.
	minRSASize = 2048
)

// validECDSASizes are the NIST curves keypair.NewECDSAKeyPair understands.
var validECDSASizes = []int{256, 384, 521}

// fipsApprovedKeyTypes are the algorithms that SSH implementations running
// in FIPS mode generally accept.
//
// ed25519 is deliberately absent. It only entered FIPS 186-5 in 2023, and
// several FIPS policies still reject ssh-ed25519 outright — so a key
// generated with it may simply be unusable against a FIPS-mode server, even
// though the algorithm itself is excellent.
var fipsApprovedKeyTypes = []SSHKeyType{SSHKeyTypeECDSA, SSHKeyTypeRSA}

// FIPSEnabled reports whether FIPS steering is in effect.
//
// An explicit `fips` setting always wins. When it's unset, this follows the
// Go runtime: a binary built and running in FIPS 140-3 mode shouldn't
// generate a key it can't itself use, and inferring that saves operators
// from having to say so twice.
func (c *Config) FIPSEnabled() bool {
	if c.FIPS != nil {
		return *c.FIPS
	}
	return fips140.Enabled()
}

// ResolveSSHKey returns the algorithm name and size to generate a keypair
// with, filling in defaults and validating what was configured. The
// algorithm is returned in the form keypair.NewSSHKeypair expects.
//
// Defaults follow FIPS mode: ECDSA P-256 when it's in effect (approved,
// widely supported, and effectively free to generate), ed25519 otherwise.
//
// FIPS mode is advisory. Configuring an algorithm it doesn't approve of
// produces a warning, not an error — the operator may well know their
// server accepts it. What it will not do is silently pick something other
// than what was asked for.
//
// Warnings are returned rather than logged so this stays free of side
// effects: NewConfig emits them once at startup, and later callers can
// resolve as often as they like without repeating them.
func (c *Config) ResolveSSHKey() (algorithm string, size int, warnings []string, err error) {
	keyType := c.SSHKey.Type
	fips := c.FIPSEnabled()

	if keyType == "" {
		if fips {
			keyType = SSHKeyTypeECDSA
		} else {
			keyType = SSHKeyTypeEd25519
		}
	} else if fips && !slices.Contains(fipsApprovedKeyTypes, keyType) {
		warnings = append(warnings, fmt.Sprintf(
			"sshkey.type %q is not FIPS-approved and a server in FIPS mode may reject it; approved types are %q and %q",
			keyType, SSHKeyTypeECDSA, SSHKeyTypeRSA))
	}

	size = c.SSHKey.Size

	switch keyType {
	case SSHKeyTypeEd25519:
		// ed25519 has exactly one size. Say so rather than ignoring a
		// configured value silently, so nobody believes they tuned it.
		if size != 0 {
			warnings = append(warnings, fmt.Sprintf(
				"sshkey.size %d is ignored for ed25519, which has a single fixed key size", size))
		}
		return string(SSHKeyTypeEd25519), 0, warnings, nil

	case SSHKeyTypeECDSA:
		if size == 0 {
			size = defaultECDSASize
		}
		if !slices.Contains(validECDSASizes, size) {
			return "", 0, warnings, fmt.Errorf("sshkey.size %d is not a valid ECDSA curve, expected one of %v", size, validECDSASizes)
		}
		return string(SSHKeyTypeECDSA), size, warnings, nil

	case SSHKeyTypeRSA:
		if size == 0 {
			size = defaultRSASize
		}
		if size < minRSASize {
			return "", 0, warnings, fmt.Errorf("sshkey.size %d is too small for RSA, minimum is %d", size, minRSASize)
		}
		return string(SSHKeyTypeRSA), size, warnings, nil

	default:
		return "", 0, warnings, fmt.Errorf("unsupported sshkey.type %q, expected one of %q, %q, or %q",
			keyType, SSHKeyTypeEd25519, SSHKeyTypeECDSA, SSHKeyTypeRSA)
	}
}
