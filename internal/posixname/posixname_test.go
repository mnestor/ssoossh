package posixname

import (
	"errors"
	"strings"
	"testing"
)

// TestValid covers each relaxed rule from systemd's valid_user_group_name()
// plus the byte cap, in both directions.
func TestValid(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"should accept a plain lower-case name", "mike", ""},
		{"should accept upper case and dots when a directory uses them", "Mike.Nestor", ""},
		{"should accept an at sign when sssd-style names are used", "mike@example", ""},
		{"should accept an inner space when a Windows-style name has one", "mike nestor", ""},
		{"should accept a leading digit when the rest is not numeric", "1mike", ""},
		{"should accept a plus sign before digits when parse_uid refuses it", "+123", ""},
		{"should accept a leading dash when letters follow", "-mike", ""},
		{"should accept multi-byte UTF-8 when it is valid", "jörg", ""},
		{"should accept exactly 32 bytes when at the cap", strings.Repeat("a", 32), ""},
		{"should accept three dots when only . and .. are special", "...", ""},
		{"should reject the empty string when nothing is given", "", "empty"},
		{"should reject 33 bytes when over the cap", strings.Repeat("a", 33), "more than 32"},
		{"should reject a leading space when padding is left", " mike", "leading or trailing space"},
		{"should reject a trailing space when padding is left", "mike ", "leading or trailing space"},
		{"should reject a lone space when it is only padding", " ", "leading or trailing space"},
		{"should reject invalid UTF-8 when a byte is stray", "mi\xffke", "not valid UTF-8"},
		{"should reject a newline when it would split the record", "mi\nke", "control character"},
		{"should reject a tab when it is a control byte", "mi\tke", "control character"},
		{"should reject DEL when it is a control byte", "mi\x7fke", "control character"},
		{"should reject a colon when it is the field separator", "mi:ke", "':' or '/'"},
		{"should reject a slash when it would escape a home path", "mi/ke", "':' or '/'"},
		{"should reject all digits when it reads as a uid", "1234", "all digits"},
		{"should reject all digits when it is outside the uid range", "99999999999", "all digits"},
		{"should reject a negative number when it reads as a uid", "-1", "negative number"},
		{"should reject a lone dash when systemd's charset test is true for the empty rest", "-", "negative number"},
		{"should reject a dot when it names the current directory", ".", "'.' or '..'"},
		{"should reject two dots when it names the parent directory", "..", "'.' or '..'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Valid(tt.input)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Valid(%q) = %v, want nil", tt.input, err)
				}
				return
			}
			if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Valid(%q) = %v, want an ErrInvalid mentioning %q", tt.input, err, tt.wantErr)
			}
		})
	}
}

// TestSanitizeGECOS covers each step of the post-render safeguarding and
// the changed flag.
func TestSanitizeGECOS(t *testing.T) {
	long := strings.Repeat("a", 255) + "é" // 257 bytes, the last rune straddles the cap
	tests := []struct {
		name        string
		input       string
		fallback    string
		want        string
		wantChanged bool
	}{
		{"should keep a clean value when nothing needs changing", "Mike Nestor", "mike", "Mike Nestor", false},
		{"should replace a colon with a space when it is the field separator", "Nestor:Mike", "mike", "Nestor Mike", true},
		{"should replace a newline with a space when it is the record separator", "Mike\nNestor", "mike", "Mike Nestor", true},
		{"should replace a bare control byte with a space when one is present", "Mike\x01Nestor", "mike", "Mike Nestor", true},
		{"should replace a C1 control with a space when one is present", "Mike\u0085Nestor", "mike", "Mike Nestor", true},
		{"should collapse whitespace runs and trim when padding is present", "  Mike \t  Nestor  ", "mike", "Mike Nestor", true},
		{"should use the fallback when the value is invalid UTF-8", "Mi\xffke", "mike", "mike", true},
		{"should use the fallback when the value renders empty", "", "mike", "mike", true},
		{"should use the fallback when only separators remain", " : \n ", "mike", "mike", true},
		{"should cut on a rune boundary when the value is over the cap", long, "mike", strings.Repeat("a", 255), true},
		{"should trim a space the cut leaves when the cap lands after one", strings.Repeat("a", 255) + " bc", "mike", strings.Repeat("a", 255), true},
		{"should keep exactly 256 bytes when at the cap", strings.Repeat("b", 256), "mike", strings.Repeat("b", 256), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := SanitizeGECOS(tt.input, tt.fallback)
			if got != tt.want || changed != tt.wantChanged {
				t.Fatalf("SanitizeGECOS(%q) = (%q, %v), want (%q, %v)", tt.input, got, changed, tt.want, tt.wantChanged)
			}
		})
	}
}
