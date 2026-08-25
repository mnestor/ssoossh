//go:build !windows

package fileperm

// restrictToOwner has nothing to add away from Windows: the mode Restrict
// already applied is the access control, and it is what every tool that
// reads these files checks.
func restrictToOwner(string) error { return nil }
