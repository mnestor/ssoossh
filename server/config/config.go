// Package config defines application configuration and sets up Viper.
//
// This package centralizes configuration loading and access for the server.
package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/mnestor/ssoossh/internal/configcheck"
)

// defaultconfig holds the embedded contents of defaults.yaml, loaded into
// Viper before any config file is merged in.
//
//go:embed defaults.yaml
var defaultconfig string

// NewConfig loads and validates the ssoosshd configuration: Load, then a
// warning per unknown key, then Validate. Every mode starts through here,
// and `ssoosshd config check` runs the same Load and Validate, so the check
// refuses exactly what a start refuses.
func NewConfig(cmd *cobra.Command) (*Config, error) {
	c, err := Load(cmd)
	if err != nil {
		return nil, err
	}

	// A key nothing declares does nothing, which is exactly how a typo, or
	// a key from a newer version, goes unnoticed. Warn rather than fail: a
	// file written for a newer ssoosshd must still load.
	warnUnknownKeys(slog.Default(), c.file, c.unknownKeys)

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Load reads and decodes the ssoosshd configuration using Viper, without
// validating it.
//
// Configuration is built by layering: defaults.yaml is loaded first, then
// the preset ldap.schema names (if any), then a config file is located and
// merged on top, so any key the file sets beats the preset. The server
// reads no configuration keys from the environment.
//
// If the command's --config/-c flag is set, that file is used. Otherwise,
// the following locations are searched in order, and the first one found is used:
//  1. ./ssoosshd.yaml (current directory)
//  2. $HOME/.config/ssoosshd.yaml
//  3. $HOME/.config/ssoossh/ssoosshd.yaml
//  4. /etc/ssoosshd.yaml (system root)
//  5. /etc/ssoossh/ssoosshd.yaml (ssoossh-specific directory)
func Load(cmd *cobra.Command) (*Config, error) {
	v := viper.New()

	// set defaults
	//
	// not covered: the error branch below is unreachable. defaultconfig is
	// defaults.yaml, embedded at compile time and checked into the repo as
	// valid YAML.
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewBufferString(defaultconfig)); err != nil {
		return nil, fmt.Errorf("failed to load embedded default config: %w", err)
	}

	configFile, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, fmt.Errorf("failed to read config flag: %w", err)
	}

	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("ssoosshd")
		v.SetConfigType("yaml")

		v.AddConfigPath(".")
		home, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(home, ".config"))
			v.AddConfigPath(filepath.Join(home, ".config", "ssoossh"))
		}
		v.AddConfigPath("/etc")
		v.AddConfigPath("/etc/ssoossh")
	}

	if err := v.MergeInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	v, err = withLDAPSchemaPreset(v)
	if err != nil {
		return nil, err
	}

	var c Config
	var decoded mapstructure.Metadata
	if err := v.Unmarshal(&c, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		// viper's defaults, which Unmarshal replaces wholesale when a hook
		// is supplied. Dropping either silently breaks every duration and
		// comma-separated list in the file.
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
		ldapFieldShorthandHook,
		autoPrivateGroupsHook,
	)), func(dc *mapstructure.DecoderConfig) { dc.Metadata = &decoded }); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// defaults.yaml and the presets declare only real keys (their tests
	// decode them exactly), so whatever is left over came from the config
	// file.
	c.unknownKeys = findUnknownKeys(decoded.Unused)
	c.file = v.ConfigFileUsed()
	c.schema = v.GetString("ldap.schema")
	return &c, nil
}

// ConfigFile is the config file Load read. Empty for a configuration
// built any other way.
func (c *Config) ConfigFile() string { return c.file }

// SectionError is one block's validation failure, named for the top-level
// key it is under (or "server" for the root keys), so a report can say
// where each of several problems is. Its text is the block's own error.
type SectionError struct {
	// Section is the block: http, cert_options, ldap and so on.
	Section string
	// Err is what the block's Validate returned.
	Err error
}

// Error implements error with the block's own message, unchanged.
func (e *SectionError) Error() string { return e.Err.Error() }

// Unwrap gives errors.Is and errors.As the block's error.
func (e *SectionError) Unwrap() error { return e.Err }

// Validate checks every block and returns all of their failures together,
// each a *SectionError, rather than stopping at the first: a refused start
// names every problem, and `config check` lists them one by one
// (configcheck.Split). Each block still stops at its own first problem.
func (c *Config) Validate() error {
	var errs []error
	check := func(section string, err error) {
		if err != nil {
			errs = append(errs, &SectionError{Section: section, Err: err})
		}
	}

	// Fail at startup rather than at first login: a bad public_url does not
	// stop anything booting, it just produces a redirect URI the identity
	// provider refuses.
	check("http", c.HTTP.Validate())

	// Same reasoning: a zero client_timeout boots fine and then misbehaves
	// in the sweep and the resolved-outcome cache, both far from the config.
	check("cert_options", c.CertOptions.Validate())

	// Validate the signer subset (broker + CA key) before first use. Every
	// mode signs or forwards signing jobs, so this is unconditional - and
	// an empty ssh_key used to surface much later as the services init
	// failing with a bare "ssh: no key found". Signer.Validate checks the
	// pubsub block first and stops there; asking it first here only puts
	// that failure under the block it is in, since the rest of the signer's
	// keys are at the root of the file.
	if err := c.Signer.PubSub.Validate(); err != nil {
		check("pubsub", err)
	} else {
		check(rootSection, c.Signer.Validate())
	}

	// Same again for the mail relay: a bad address or an unreadable
	// password file boots fine and then goes unnoticed, because a
	// notification that never arrives looks exactly like one that was
	// never triggered.
	check("mail", c.Mail.Validate())

	// Same again for the directory: an LDAP block that cannot work is
	// better caught here than at the first login, where it would silently
	// fall back to OIDC-only enrichment (LDAP fails open by design, which
	// is exactly what would hide a typo).
	check("ldap", c.LDAP.Validate())

	// And the NSS directory, which reads every account from LDAP: a gecos
	// template typo or an unparseable source allow list would otherwise
	// surface only at the first host lookup, far from the config.
	check("nss", c.NSS.Validate(&c.LDAP))

	// And the support contact: it is rendered as a link on the login page,
	// so a bad address surfaces to a locked-out user rather than to anyone
	// who could fix it.
	check("branding", c.Branding.Validate())

	// Require an explicit cookie_key when multi-instance is enabled, so
	// sessions don't break between instances. The default per-process
	// random key would leave users logging out unexpectedly.
	if c.MultiInstance && c.HTTP.CookieKey == "" {
		check(rootSection, fmt.Errorf("multi_instance is enabled but http.cookie_key is not set: set an explicit key or disable multi_instance"))
	}

	return configcheck.Join(errs...)
}

// rootSection names the keys at the root of the file, which have no block
// of their own: the admin effective-config view groups them the same way.
const rootSection = "server"

// SectionWarning is a Warning tagged with the block it is about.
type SectionWarning struct {
	// Section is the block, as in SectionError.
	Section string
	Warning
}

// Warnings returns the advice a start logs about a configuration that is
// valid but weaker than it should be, from the same functions the start
// logs it from: the session cookie's Secure flag (http, in the modes that
// serve the web UI) and the mail relay. Warnings that Validate logs as it
// runs are not repeated here.
func (c *Config) Warnings() []SectionWarning {
	var out []SectionWarning
	for _, w := range c.HTTP.Warnings() {
		out = append(out, SectionWarning{Section: "http", Warning: w})
	}
	for _, w := range c.Mail.Warnings() {
		out = append(out, SectionWarning{Section: "mail", Warning: w})
	}
	return out
}

// withLDAPSchemaPreset slides the preset ldap.schema names in between
// defaults.yaml and the config file, returning v unchanged when the file
// names none.
//
// The schema is read from the file itself, so the file has to be read
// before the layer beneath it can be built: v already holds defaults plus
// the file, and the rebuild reads that same file (ConfigFileUsed) again on
// top of defaults plus the preset. Which file is used and how it is parsed
// are therefore exactly what the first read decided. A file edited between
// the two reads to name another schema fails rather than loading a mix.
func withLDAPSchemaPreset(v *viper.Viper) (*viper.Viper, error) {
	schema := v.GetString("ldap.schema")
	if schema == "" {
		return v, nil
	}
	preset, err := ldapSchemaPreset(schema)
	if err != nil {
		return nil, err
	}

	layered := viper.New()
	layered.SetConfigType("yaml")
	if err := layered.ReadConfig(bytes.NewBufferString(defaultconfig)); err != nil {
		// not covered: unreachable, for the reason NewConfig gives.
		return nil, fmt.Errorf("failed to load embedded default config: %w", err)
	}
	if err := layered.MergeConfig(bytes.NewReader(preset)); err != nil {
		// not covered: unreachable. The presets are embedded, and
		// TestLDAPSchemaPresets_ShouldLoad parses every one.
		return nil, fmt.Errorf("failed to load the %s schema preset: %w", schema, err)
	}
	layered.SetConfigFile(v.ConfigFileUsed())
	if err := layered.MergeInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	if again := layered.GetString("ldap.schema"); again != schema {
		return nil, fmt.Errorf("the config file %s changed while it was loading (ldap.schema %q, then %q): start again", v.ConfigFileUsed(), schema, again)
	}
	return layered, nil
}
