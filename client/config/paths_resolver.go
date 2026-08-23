package config

// ResolvePaths is a documented extension point for config post-processing.
// It is called by NewConfig after unmarshaling, allowing defaults to be
// derived from other config or the environment rather than being compile-time
// constants. Currently, all path configuration is handled via CLI flags rather
// than config files; this function is retained for future extension.
func (c *Config) ResolvePaths() error {
	return nil
}
