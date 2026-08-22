package service

import (
	"testing"
)

// FuzzParseLifetimePolicy tests lifetime policy duration parsing for robustness
// against malformed input, extreme values, overflow, and denial of service.
func FuzzParseLifetimePolicy(f *testing.F) {
	// Valid durations
	f.Add("1h", "user")
	f.Add("24h", "host")
	f.Add("30m", "service")
	f.Add("1h30m", "pam")

	// Edge cases
	f.Add("0h", "valid_zero")
	f.Add("1s", "one_second")
	f.Add("999999h", "large")

	// Malformed durations
	f.Add("", "empty")
	f.Add("xyz", "invalid")
	f.Add("-1h", "negative")
	f.Add("1.5h", "float")
	f.Add("1h30m45s", "multiple")

	// Extreme values
	f.Add("999999999999999999999h", "huge")

	// With spaces
	f.Add(" 1h ", "spaces")
	f.Add("1 h", "space_in_unit")

	f.Fuzz(func(t *testing.T, policyStr string, typeName string) {
		// parseLifetimePolicy should not panic on any input
		// and should complete in reasonable time
		policy, err := parseLifetimePolicy(typeName, policyStr)

		if err != nil {
			// Error is acceptable for malformed input
			return
		}

		// If we got a policy, it should have sensible values
		if policy != nil {
			// Duration should be positive (zero is odd but not a crash)
			if policy.DefaultMax < 0 {
				t.Fatalf("parsed policy has negative duration: %v", policy.DefaultMax)
			}
		}
	})
}
