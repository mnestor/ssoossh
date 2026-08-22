package ssh

import (
	"strings"
	"testing"
)

// TestValidatePrincipal tests the principal validation function with a table
// of test cases covering edge cases: empty, whitespace, newlines, commas,
// shell metacharacters, and exact max length.
func TestValidatePrincipal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		principal string
		wantError bool
	}{
		// Valid cases
		{
			name:      "simple username",
			principal: "alice",
			wantError: false,
		},
		{
			name:      "username with underscore",
			principal: "alice_bob",
			wantError: false,
		},
		{
			name:      "username with hyphen",
			principal: "alice-bob",
			wantError: false,
		},
		{
			name:      "hostname with dots",
			principal: "host.example.com",
			wantError: false,
		},
		{
			name:      "mixed valid characters",
			principal: "user_name-123.example",
			wantError: false,
		},
		{
			name:      "single character",
			principal: "a",
			wantError: false,
		},
		{
			name:      "numeric only",
			principal: "12345",
			wantError: false,
		},
		{
			name:      "max length (255 chars)",
			principal: strings.Repeat("a", 255),
			wantError: false,
		},

		// Invalid cases
		{
			name:      "empty string",
			principal: "",
			wantError: true,
		},
		{
			name:      "only whitespace",
			principal: "   ",
			wantError: true,
		},
		{
			name:      "contains newline",
			principal: "alice\nbob",
			wantError: true,
		},
		{
			name:      "contains comma",
			principal: "alice,bob",
			wantError: true,
		},
		{
			name:      "contains space",
			principal: "alice bob",
			wantError: true,
		},
		{
			name:      "contains shell metachar dollar",
			principal: "alice$bob",
			wantError: true,
		},
		{
			name:      "contains shell metachar backtick",
			principal: "alice`bob",
			wantError: true,
		},
		{
			name:      "contains shell metachar pipe",
			principal: "alice|bob",
			wantError: true,
		},
		{
			name:      "contains shell metachar semicolon",
			principal: "alice;bob",
			wantError: true,
		},
		{
			name:      "contains shell metachar asterisk",
			principal: "alice*bob",
			wantError: true,
		},
		{
			name:      "contains shell metachar question",
			principal: "alice?bob",
			wantError: true,
		},
		{
			name:      "contains quote",
			principal: "alice\"bob",
			wantError: true,
		},
		{
			name:      "contains at sign",
			principal: "alice@bob",
			wantError: true,
		},
		{
			name:      "contains slash",
			principal: "alice/bob",
			wantError: true,
		},
		{
			name:      "exceeds max length (256 chars)",
			principal: strings.Repeat("a", 256),
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePrincipal(tt.principal)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidatePrincipal(%q) got error %v, want error %v", tt.principal, err, tt.wantError)
			}
		})
	}
}
