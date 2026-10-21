package config

import (
	"fmt"
	"os"

	"github.com/mnestor/ssoossh/internal/configcheck"
)

// FileError is a problem with one of the files the daemon reads at start,
// naming the file so a report can say which. Its text is the underlying
// error's, which already names the path.
type FileError struct {
	// File is the path that failed.
	File string
	// Err is what reading or checking it returned.
	Err error
}

// Error implements error.
func (e *FileError) Error() string { return e.Err.Error() }

// Unwrap gives errors.Is and errors.As the underlying error.
func (e *FileError) Unwrap() error { return e.Err }

// LoadStartup reads everything the daemon reads before it opens a socket
// or the cache: nss.yaml (Load), the host override file (LoadOverrides)
// and the fleet key file nss.yaml names (ReadKeyFile), each with its mode
// and owner checks. It returns every failure, each a *FileError, joined
// with configcheck.Join, rather than the first.
//
// The daemon refuses to start on any error; `ssoossh-nss config check`
// lists them. The Config is returned whenever nss.yaml decoded, even when
// it or another file failed, so the check can still report its warnings;
// a caller must not run on it unless the error is nil. The key's secret is
// read but never returned or printed.
func LoadStartup(configPath, overridesPath string) (*Config, error) {
	return loadStartup(configPath, overridesPath, os.Geteuid())
}

// loadStartup is LoadStartup with the effective uid supplied, so a test
// can exercise the root-only owner checks.
func loadStartup(configPath, overridesPath string, euid int) (*Config, error) {
	var errs []error
	// Decoded and validated apart, as load does in one step, so an
	// invalid nss.yaml still names the key file to check.
	cfg, err := decode(configPath, euid)
	if err != nil {
		errs = append(errs, &FileError{File: configPath, Err: err})
	} else if err := cfg.Validate(); err != nil {
		errs = append(errs, &FileError{File: configPath, Err: fmt.Errorf("%s: %w", configPath, err)})
	}
	// A broken override file refuses the start; once running, a later
	// broken edit keeps the last good overrides instead.
	if _, err := loadOverrides(overridesPath, euid); err != nil {
		errs = append(errs, &FileError{File: overridesPath, Err: err})
	}
	// The key file is only known once nss.yaml loaded. upstream.New reads
	// it again for the daemon; reading it here as well is what lets a
	// check refuse a key the start would refuse.
	if cfg != nil {
		if _, err := readKeyFile(cfg.APIKeyFile, euid); err != nil {
			errs = append(errs, &FileError{File: cfg.APIKeyFile, Err: fmt.Errorf("fleet key: %w", err)})
		}
	}
	return cfg, configcheck.Join(errs...)
}
