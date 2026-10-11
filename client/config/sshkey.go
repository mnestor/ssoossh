package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mnestor/ssoossh/internal/fipsmode"
)

// FIPSEnabled reports whether FIPS steering is in effect. See
// fipsmode.Enabled.
func (c *Config) FIPSEnabled() bool {
	return fipsmode.Enabled(c.FIPS)
}

// ResolveSSHKey returns the algorithm name and size to generate a keypair
// with, filling in defaults and validating what was configured. The
// algorithm is returned in the form keypair.NewSSHKeypair expects.
//
// The default is ECDSA P-384 unconditionally, whether or not FIPS is in
// effect.
//
// When FIPS is in effect, configuring a non-approved algorithm is a hard
// error: an operator with FIPS enabled is asking for that to actually be
// enforced. The only way around it is to disable FIPS itself
// (Config.FIPS = false), not to pick an unapproved key type while FIPS
// stays on.
//
// Warnings are returned rather than logged so this stays free of side
// effects: NewConfig emits them once at startup, and later callers can
// resolve as often as they like without repeating them.
func (c *Config) ResolveSSHKey() (algorithm string, size int, warnings []string, err error) {
	keyType := c.SSHKey.Type
	fips := c.FIPSEnabled()

	if keyType == "" {
		keyType = fipsmode.DefaultSSHKeyType()
	} else if fips && !fipsmode.IsApprovedInFIPS(keyType) {
		return "", 0, warnings, fmt.Errorf(
			"key type %q (--key-type or sshkey.type) is not FIPS-approved while fips is on; use %s or %s",
			keyType, fipsmode.SSHKeyTypeECDSA, fipsmode.SSHKeyTypeRSA)
	}

	size = c.SSHKey.Size

	switch keyType {
	case SSHKeyTypeEd25519:
		// ed25519 has exactly one size. Say so rather than ignoring a
		// configured value silently, so nobody believes they tuned it.
		if size != 0 {
			warnings = append(warnings, fmt.Sprintf(
				"key size %d (--key-size or sshkey.size) is ignored for ed25519, which has a single fixed key size", size))
		}
		return string(SSHKeyTypeEd25519), 0, warnings, nil

	case SSHKeyTypeECDSA:
		if size == 0 {
			size = fipsmode.DefaultSizeForAlgorithm(SSHKeyTypeECDSA)
		}
		validSizes := fipsmode.ValidECDSASizes()
		if !slices.Contains(validSizes, size) {
			return "", 0, warnings, fmt.Errorf("key size %d (--key-size or sshkey.size) is not an ECDSA curve; use one of %s",
				size, joinInts(validSizes))
		}
		return string(SSHKeyTypeECDSA), size, warnings, nil

	case SSHKeyTypeRSA:
		if size == 0 {
			size = fipsmode.DefaultSizeForAlgorithm(SSHKeyTypeRSA)
		}
		if size < fipsmode.MinRSASize() {
			return "", 0, warnings, fmt.Errorf("key size %d (--key-size or sshkey.size) is too small for RSA; the minimum is %d",
				size, fipsmode.MinRSASize())
		}
		return string(SSHKeyTypeRSA), size, warnings, nil

	default:
		return "", 0, warnings, fmt.Errorf("unsupported key type %q (--key-type or sshkey.type); use %s, %s or %s",
			keyType, SSHKeyTypeECDSA, SSHKeyTypeEd25519, SSHKeyTypeRSA)
	}
}

// joinInts lists sizes for an error message: "256, 384 or 521".
func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}
