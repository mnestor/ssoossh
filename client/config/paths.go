package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// appDirName is the per-application directory used inside whichever
// system/user configuration root the platform provides.
const appDirName = "ssoossh"

// configFileName is the configuration file's name in every location.
const configFileName = "ssoossh.yaml"

// searchPaths are the locations NewConfig merges, in increasing order of
// precedence. Separated from the loading logic so the platform rules below
// can be tested without touching the real filesystem, and so tests can run
// against temporary directories instead of whatever the developer happens to
// have in their home directory.
type searchPaths struct {
	// systemDir is the machine-wide configuration directory. It holds
	// ssoossh.yaml and is the only place `enforce` is read from — and where
	// a relative `enforce` target resolves — so on every platform it must be
	// a location ordinary users cannot write to.
	systemDir string

	// userFile is the per-user configuration file. Empty when the platform
	// gives no usable answer (no home directory), in which case it is simply
	// skipped.
	userFile string

	// localFile is the configuration in the working directory, for
	// development.
	localFile string
}

// defaultSearchPaths returns the locations for the running platform.
func defaultSearchPaths() searchPaths {
	return searchPaths{
		systemDir: systemConfigDir(runtime.GOOS, os.Getenv("ProgramData")),
		userFile:  userConfigFile(runtime.GOOS, homeDir(), os.Getenv("AppData")),
		localFile: "./" + configFileName,
	}
}

// homeDir returns the user's home directory, or "" if it cannot be
// determined — a machine account or a stripped environment, where having no
// per-user configuration is the correct answer rather than an error.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// systemConfigDir returns the machine-wide configuration directory for goos.
//
// On Windows that is %ProgramData%\ssoossh — the platform's equivalent of
// /etc, and the only common location that is administrator-writable but not
// user-writable, which is what `enforce` depends on. See the sample config
// for the caveat: ProgramData's default ACLs let any user *create* a
// subdirectory, and whoever creates one owns it. The installer must create
// this directory, or a non-administrator could create it first and own the
// file that is supposed to constrain them.
//
// %ProgramData% is set on every supported Windows version; the literal
// fallback exists only so a stripped environment degrades to the standard
// location rather than to a relative path, which would resolve against the
// working directory — user-controlled, and exactly what must not happen.
func systemConfigDir(goos, programData string) string {
	if goos != "windows" {
		return "/etc/" + appDirName
	}
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, appDirName)
}

// userConfigFile returns the per-user configuration file for goos.
//
// On Windows that is %AppData%\ssoossh\ssoossh.yaml — the roaming profile, so
// the setting follows the user between domain-joined machines, which is what
// a per-user preference should do.
//
// Elsewhere it stays ~/.config/ssoossh.yaml. Deliberately not os.UserConfigDir():
// on macOS that returns ~/Library/Application Support, and moving an existing
// installation's configuration is a worse outcome than being inconsistent
// with a convention that command-line tools on macOS mostly ignore anyway.
// (It also means XDG_CONFIG_HOME is not honored on Linux, which is a separate
// question from this one.)
func userConfigFile(goos, home, appData string) string {
	if goos == "windows" {
		if appData == "" {
			if home == "" {
				return ""
			}
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, appDirName, configFileName)
	}

	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", configFileName)
}
