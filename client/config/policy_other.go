//go:build !windows && !darwin

package config

// loadPlatformPolicy is a no-op on Linux (and any other unlisted GOOS): the
// existing enforce-file mechanism (see NewConfig) already provides an
// admin-only-writable lock, matching how Linux fleets are normally
// provisioned. See https://mnestor.github.io/ssoossh/hosts/client-enforcement/.
func loadPlatformPolicy() (map[string]any, error) {
	return nil, nil
}
