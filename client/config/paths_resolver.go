package config

// ResolvePaths fills in default paths for file-based configuration entries.
// Called by NewConfig after unmarshaling, so a default can be derived from
// other config or the environment rather than being a compile-time constant.
func (c *Config) ResolvePaths() error {
	if c.PrincipalMappingFile == "" {
		c.PrincipalMappingFile = "/etc/ssoossh/principals.json"
	}

	return nil
}
