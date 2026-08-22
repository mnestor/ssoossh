// Package serial holds certificate serial number allocation logic shared
// between server/service (where serials are reserved at approval time) and
// server/signer (where they are used at signing time).
package serial

import (
	"crypto/rand"
	"fmt"
)

// Mask clears the high bit of a generated serial. Go's database/sql refuses
// to bind a uint64 with the high bit set ("uint64 values with high bit set
// are not supported") because it has no lossless signed equivalent — so a
// full-width random serial would fail to persist as an audit row roughly
// half the time, and the certificate would never reach the client. 63 bits
// of randomness is still far more than enough to keep collisions negligible.
const Mask = 1<<63 - 1

// New returns a random certificate serial. Random rather than a counter so it
// needs no coordination — the signer has no database, and there may
// eventually be several signers with independent hardware-backed keys (see
// docs/certificate-lifetime-policy-plan.md's note on multiple signers).
// Serials matter for revocation lists (KRLs).
//
// The returned serial is guaranteed to have the high bit clear (Mask applied),
// but may be zero. Code treating zero as a sentinel must check explicitly;
// the allocation does not reserve zero as "unset."
func New() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("failed to generate certificate serial: %w", err)
	}
	// Convert bytes to uint64 in big-endian, then apply mask to clear high bit.
	serial := uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
	return serial & Mask, nil
}
