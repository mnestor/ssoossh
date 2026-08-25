//go:build darwin

package config

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

// withManagedPreferencesDir points managedPreferencesDir at a temp
// directory for the duration of the test, so tests don't touch the real
// /Library or require running as an MDM-enrolled user.
func withManagedPreferencesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := managedPreferencesDir
	managedPreferencesDir = dir
	t.Cleanup(func() { managedPreferencesDir = orig })
	return dir
}

func writePlist(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 0600 rather than the 0644 a real managed plist carries: the loader
	// never looks at the mode, the test reads the file back as the same
	// user, and a world-readable fixture is the one thing here that would
	// trip gosec's G306 on the macOS lint pass.
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write plist: %v", err)
	}
}

func TestLoadPlatformPolicy_ShouldReturnNilWhenNeitherFileExists(t *testing.T) {
	withManagedPreferencesDir(t)

	policy, err := loadPlatformPolicy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy != nil {
		t.Errorf("got %#v, want nil when nothing is managed", policy)
	}
}

func TestLoadPlatformPolicy_ShouldReadTheDeviceScopedFile(t *testing.T) {
	dir := withManagedPreferencesDir(t)
	writePlist(t, filepath.Join(dir, preferenceDomain+".plist"), `<plist version="1.0"><dict>
		<key>server</key><string>https://device.example.com</string>
	</dict></plist>`)

	policy, err := loadPlatformPolicy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy["server"] != "https://device.example.com" {
		t.Errorf("got %#v, want server from the device-scoped file", policy)
	}
}

func TestLoadPlatformPolicy_ShouldLetTheUserScopedFileWinOverTheDeviceScopedFile(t *testing.T) {
	dir := withManagedPreferencesDir(t)
	u, err := user.Current()
	if err != nil {
		t.Skipf("cannot determine current user: %v", err)
	}

	writePlist(t, filepath.Join(dir, preferenceDomain+".plist"), `<plist version="1.0"><dict>
		<key>server</key><string>https://device.example.com</string>
		<key>fips</key><true/>
	</dict></plist>`)
	writePlist(t, filepath.Join(dir, u.Username, preferenceDomain+".plist"), `<plist version="1.0"><dict>
		<key>server</key><string>https://user.example.com</string>
	</dict></plist>`)

	policy, err := loadPlatformPolicy()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy["server"] != "https://user.example.com" {
		t.Errorf("got server %#v, want the user-scoped value to win", policy["server"])
	}
	if policy["fips"] != true {
		t.Errorf("got %#v, want the device-scoped fips:true preserved since the user file didn't set it", policy)
	}
}
