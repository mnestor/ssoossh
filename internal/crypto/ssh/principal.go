package ssh

import (
	"fmt"
	"regexp"
)

// principalRegex validates SSH principal names: alphanumeric, dot, underscore,
// hyphen only. Hostnames can contain dots, which
// are allowed here; future hostname-specific validation can enforce additional
// constraints if needed.
var principalRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// ValidatePrincipal checks whether principal is a valid SSH principal name:
// non-empty, at most 255 characters, and matching the allowed character set
// (alphanumeric, dot, underscore, hyphen). Returns nil if valid, an error if
// invalid.
func ValidatePrincipal(principal string) error {
	if principal == "" {
		return fmt.Errorf("principal cannot be empty")
	}
	if len(principal) > 255 {
		return fmt.Errorf("principal exceeds maximum length of 255 characters")
	}
	if !principalRegex.MatchString(principal) {
		return fmt.Errorf("principal contains invalid characters: %q", principal)
	}
	return nil
}
