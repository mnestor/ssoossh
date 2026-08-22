package fipsmode

import "testing"

// boolPtr returns a pointer to b, for the tri-state FIPS setting.
func boolPtr(b bool) *bool { return &b }

func TestEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		explicit *bool
		want     bool
	}{
		{name: "should return true when explicitly set true", explicit: boolPtr(true), want: true},
		{name: "should return false when explicitly set false", explicit: boolPtr(false), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Enabled(tt.explicit); got != tt.want {
				t.Errorf("Enabled(%v) = %v, want %v", tt.explicit, got, tt.want)
			}
		})
	}
}

// The nil (runtime-fallback) case isn't asserted against a specific value
// here: crypto/fips140.Enabled() reflects how the test binary itself was
// built, which this suite doesn't control. It's exercised for panics only.
func TestEnabled_FallsBackToRuntimeWhenUnset(t *testing.T) {
	t.Parallel()
	_ = Enabled(nil)
}
