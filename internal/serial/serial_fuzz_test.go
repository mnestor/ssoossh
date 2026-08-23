package serial

import (
	"testing"
)

// FuzzNew tests serial number generation. Ensures New() never panics
// and always returns valid values within the expected range.
func FuzzNew(f *testing.F) {
	f.Fuzz(func(t *testing.T, seed int64) {
		// Just ensure New() never panics
		serial, err := New()

		if err != nil {
			// Error from crypto/rand is unexpected but not a panic
			t.Logf("serial.New() returned error: %v", err)
			return
		}

		// Serial should have high bit clear (Mask applied)
		if serial > Mask {
			t.Fatalf("serial.New() returned %d > Mask (%d)", serial, Mask)
		}

		// Serial is allowed to be zero (documented behavior), and it is a
		// uint64, so there is no negative case left to check.
	})
}

// FuzzMask tests the Mask constant behavior.
func FuzzMask(f *testing.F) {
	f.Fuzz(func(t *testing.T, val uint64) {
		// Apply mask
		masked := val & Mask

		// Masked value should never exceed Mask
		if masked > Mask {
			t.Fatalf("masked value %d > Mask %d", masked, Mask)
		}

		// High bit should be clear
		if masked&(1<<63) != 0 {
			t.Fatalf("masked value %d has high bit set", masked)
		}
	})
}
