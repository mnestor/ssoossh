//go:build darwin

package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// preferenceDomain is the macOS preference domain (and plist filename, sans
// extension) a configuration profile targets to lock ssoossh client
// settings — the same identifier an MDM's Custom Settings/Managed
// Preferences payload names.
const preferenceDomain = "com.github.mnestor.ssoossh"

// managedPreferencesDir is where macOS materializes managed-preferences
// plists for an installed configuration profile. A package-level var
// (rather than a literal in loadPlatformPolicy) so tests can point it at a
// temp directory instead of the real /Library.
var managedPreferencesDir = "/Library/Managed Preferences"

// loadPlatformPolicy reads locked ssoossh settings from macOS managed
// preferences. A "Device"-scoped configuration profile materializes
// <managedPreferencesDir>/<preferenceDomain>.plist; a "User"-scoped one
// materializes <managedPreferencesDir>/<username>/<preferenceDomain>.plist.
// Neither path is a documented public API (the sanctioned entry point is
// CFPreferencesCopyAppValue, which needs CGo and a macOS SDK this project's
// cross-compiling CI doesn't have — see docs/client-settings-enforcement.md),
// but the location has been stable for years and is what other enterprise
// Mac tooling already relies on directly. When both files exist, the
// user-scoped one wins for any key it sets, matching Apple's documented
// precedence of the user channel over the device channel.
func loadPlatformPolicy() (map[string]any, error) {
	flat := map[string]any{}

	devicePath := filepath.Join(managedPreferencesDir, preferenceDomain+".plist")
	if err := mergePlistFile(flat, devicePath); err != nil {
		return nil, err
	}

	if u, err := user.Current(); err == nil && u.Username != "" {
		userPath := filepath.Join(managedPreferencesDir, u.Username, preferenceDomain+".plist")
		if err := mergePlistFile(flat, userPath); err != nil {
			return nil, err
		}
	}

	if len(flat) == 0 {
		return nil, nil
	}
	return buildPolicyMap(flat), nil
}

// mergePlistFile reads path, if it exists, and merges its values into dst
// — later merges win, which is how loadPlatformPolicy gives the user-scoped
// file precedence over the device-scoped one.
func mergePlistFile(dst map[string]any, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}

	values, err := parsePolicyPlist(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for k, v := range values {
		dst[k] = v
	}
	return nil
}
