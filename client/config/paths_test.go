package config

import (
	"path/filepath"
	"testing"
)

// The goos parameter is what makes these testable at all: the rules for a
// platform can be checked from any host, rather than only on that platform.

func TestSystemConfigDir(t *testing.T) {
	tests := []struct {
		name        string
		goos        string
		programData string
		want        string
	}{
		{
			name: "should use /etc on linux",
			goos: "linux",
			want: "/etc/ssoossh",
		},
		{
			name: "should use /etc on macos",
			goos: "darwin",
			want: "/etc/ssoossh",
		},
		{
			name:        "should use ProgramData on windows",
			goos:        "windows",
			programData: `C:\ProgramData`,
			want:        filepath.Join(`C:\ProgramData`, "ssoossh"),
		},
		{
			name:        "should honor a relocated ProgramData",
			goos:        "windows",
			programData: `D:\Data`,
			want:        filepath.Join(`D:\Data`, "ssoossh"),
		},
		{
			// A relative path here would resolve against the working
			// directory, which any user controls — and this is the directory
			// the `enforce` lock is read from.
			name: "should fall back to an absolute path when ProgramData is unset",
			goos: "windows",
			want: filepath.Join(`C:\ProgramData`, "ssoossh"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := systemConfigDir(tt.goos, tt.programData); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUserConfigFile(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		home    string
		appData string
		want    string
	}{
		{
			name: "should use ~/.config on linux",
			goos: "linux",
			home: "/home/alice",
			want: filepath.Join("/home/alice", ".config", "ssoossh.yaml"),
		},
		{
			// Deliberately not ~/Library/Application Support: moving an
			// existing installation's configuration would be worse than
			// diverging from a convention macOS CLI tools mostly ignore.
			name: "should use ~/.config on macos too",
			goos: "darwin",
			home: "/Users/alice",
			want: filepath.Join("/Users/alice", ".config", "ssoossh.yaml"),
		},
		{
			name:    "should use the roaming profile on windows",
			goos:    "windows",
			home:    `C:\Users\alice`,
			appData: `C:\Users\alice\AppData\Roaming`,
			want:    filepath.Join(`C:\Users\alice\AppData\Roaming`, "ssoossh", "ssoossh.yaml"),
		},
		{
			name: "should derive the roaming profile from home when AppData is unset",
			goos: "windows",
			home: `C:\Users\alice`,
			want: filepath.Join(`C:\Users\alice`, "AppData", "Roaming", "ssoossh", "ssoossh.yaml"),
		},
		{
			// No home directory is a real situation for a machine account.
			// Having no per-user configuration is the right answer there,
			// not an error and certainly not a relative path.
			name: "should return nothing when there is no home directory",
			goos: "linux",
			want: "",
		},
		{
			name: "should return nothing on windows with neither AppData nor home",
			goos: "windows",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := userConfigFile(tt.goos, tt.home, tt.appData); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDefaultSearchPaths_ShouldProduceAbsoluteSystemPath guards the property
// the `enforce` lock depends on: whatever the environment looks like, the
// system directory must never be resolved against the working directory.
func TestDefaultSearchPaths_ShouldProduceAbsoluteSystemPath(t *testing.T) {
	paths := defaultSearchPaths()

	if paths.systemDir == "" {
		t.Fatal("expected a system configuration directory")
	}
	// On a Unix host the Windows branch is not exercised here; both branches
	// are covered by TestSystemConfigDir above.
	if !filepath.IsAbs(paths.systemDir) && !isWindowsAbs(paths.systemDir) {
		t.Errorf("system directory %q is not absolute", paths.systemDir)
	}
}

// isWindowsAbs reports whether p looks like a Windows absolute path, which
// filepath.IsAbs does not recognize when running on Unix.
func isWindowsAbs(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// The cache is machine state, not configuration, so it must land outside
// every directory searchPaths merges — a cached key written into the
// per-user config would outrank a capubkey an administrator later set in
// the system one.
func TestCACacheFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		goos         string
		home         string
		xdgCacheHome string
		localAppData string
		want         string
	}{
		{
			name: "should use ~/.cache on linux",
			goos: "linux", home: "/home/u",
			want: filepath.Join("/home/u", ".cache", "ssoossh", "ca.json"),
		},
		{
			name: "should use ~/.cache on macos, matching the config file's choice to ignore ~/Library",
			goos: "darwin", home: "/Users/u",
			want: filepath.Join("/Users/u", ".cache", "ssoossh", "ca.json"),
		},
		{
			name: "should honor XDG_CACHE_HOME when it is set",
			goos: "linux", home: "/home/u", xdgCacheHome: "/var/tmp/cache",
			want: filepath.Join("/var/tmp/cache", "ssoossh", "ca.json"),
		},
		{
			name: "should prefer XDG_CACHE_HOME even with no home directory",
			goos: "linux", home: "", xdgCacheHome: "/var/tmp/cache",
			want: filepath.Join("/var/tmp/cache", "ssoossh", "ca.json"),
		},
		{
			name: "should return nothing when there is no home and no XDG_CACHE_HOME",
			goos: "linux", home: "",
			want: "",
		},
		{
			name: "should use LocalAppData on windows, not the roaming profile a cache has no business in",
			goos: "windows", home: `C:\Users\u`, localAppData: `C:\Users\u\AppData\Local`,
			want: filepath.Join(`C:\Users\u\AppData\Local`, "ssoossh", "ca.json"),
		},
		{
			name: "should fall back to the standard local path when LocalAppData is missing",
			goos: "windows", home: `C:\Users\u`,
			want: filepath.Join(`C:\Users\u`, "AppData", "Local", "ssoossh", "ca.json"),
		},
		{
			name: "should return nothing on windows with neither LocalAppData nor a home",
			goos: "windows", home: "",
			want: "",
		},
		{
			name: "should ignore XDG_CACHE_HOME on windows",
			goos: "windows", home: `C:\Users\u`, xdgCacheHome: "/var/tmp/cache", localAppData: `C:\Users\u\AppData\Local`,
			want: filepath.Join(`C:\Users\u\AppData\Local`, "ssoossh", "ca.json"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := caCacheFile(tt.goos, tt.home, tt.xdgCacheHome, tt.localAppData); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
