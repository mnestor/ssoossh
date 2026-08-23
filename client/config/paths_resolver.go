package config

import (
	"os"
	"path/filepath"
)

// ResolvePaths fills in default paths for file-based configuration entries.
// Called by NewConfig after unmarshaling so defaults can reference $HOME.
func (c *Config) ResolvePaths() error {
	if c.ServiceEnrollmentFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		c.ServiceEnrollmentFile = filepath.Join(home, ".config", "ssoossh", "service_enrollment.json")
	}

	if c.PrincipalMappingFile == "" {
		c.PrincipalMappingFile = "/etc/ssoossh/principals.json"
	}

	return nil
}
