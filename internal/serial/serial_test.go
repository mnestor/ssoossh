package serial

import (
	"testing"
)

func TestNew_ShouldReturnValidSerial(t *testing.T) {
	serial, err := New()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if serial == 0 {
		t.Logf("note: zero is a valid but unlikely serial (probability 2^-63)")
	}
}

func TestNew_ShouldHaveHighBitClear(t *testing.T) {
	for i := 0; i < 100; i++ {
		serial, err := New()
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
		if serial&(1<<63) != 0 {
			t.Errorf("iteration %d: expected high bit to be clear, got serial=%d (0x%016x)",
				i, serial, serial)
		}
	}
}

func TestNew_ShouldApplyMaskConsistently(t *testing.T) {
	if Mask != 1<<63-1 {
		t.Errorf("expected Mask to be 1<<63-1, got 0x%016x", Mask)
	}
}

func TestNew_ShouldReturnDifferentSerials(t *testing.T) {
	serials := make(map[uint64]bool)
	for i := 0; i < 100; i++ {
		serial, err := New()
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
		if serials[serial] {
			t.Errorf("iteration %d: got duplicate serial %d", i, serial)
		}
		serials[serial] = true
	}
	if len(serials) < 99 {
		t.Errorf("expected at least 99 unique serials from 100 draws, got %d", len(serials))
	}
}
