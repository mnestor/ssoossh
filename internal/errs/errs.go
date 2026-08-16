// Package errs holds shared error types used across client, server, and
// pam_ssoossh — not just scaffolding placeholders, but a home for any error
// that needs consistent identity/behavior wherever it's raised.
package errs

import "fmt"

// NotImplementedError indicates a command or route exists as a scaffold but
// its logic hasn't been implemented yet. What identifies the specific
// command/route (e.g. "ssh login"); it's optional.
type NotImplementedError struct {
	What string
}

// Error implements the error interface.
func (e *NotImplementedError) Error() string {
	if e.What == "" {
		return "not implemented"
	}
	return fmt.Sprintf("%s: not implemented", e.What)
}

// Is reports whether target is a *NotImplementedError, regardless of What,
// so callers can use errors.Is(err, &errs.NotImplementedError{}) as a
// sentinel check.
func (e *NotImplementedError) Is(target error) bool {
	_, ok := target.(*NotImplementedError)
	return ok
}
