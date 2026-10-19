// Package config loads and validates ssoosshd-nss's configuration,
// /etc/ssoossh/nss.yaml, together with the fleet key file and the host
// override file it names. Option names follow sssd.conf(5), as Part 3 of
// docs/proposals/nss-directory says.
//
// Only the file is read: the environment is never consulted, so nothing a
// caller's environment says can change which server the host trusts or how
// it maps names to ids. The one exception is "tls_ca_file: system", where
// Go builds the system trust store and honours SSL_CERT_FILE and
// SSL_CERT_DIR from the daemon's own environment, which its unit decides.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"

	"github.com/mnestor/ssoossh/internal/posixname"
)

// Default paths. The server package already owns /run/ssoossh and
// /var/lib/ssoossh (user ssoossh, mode 0750), so the daemon keeps its
// runtime and state files in directories of its own, matching the unit
// directives RuntimeDirectory=ssoossh-nss and StateDirectory=ssoossh-nss.
// The configuration stays beside the rest of ssoossh's in /etc/ssoossh.
const (
	// DefaultConfigPath is the daemon's configuration file.
	DefaultConfigPath = "/etc/ssoossh/nss.yaml"
	// DefaultKeyPath is the fleet key file, api_key_file's default.
	DefaultKeyPath = "/etc/ssoossh/nss.key"
	// DefaultOverridesPath is the host override file.
	DefaultOverridesPath = "/etc/ssoossh/nss-overrides.yaml"
	// DefaultCachePath is the persistent cache's SQLite file.
	DefaultCachePath = "/var/lib/ssoossh-nss/cache.db"
	// DefaultAdminSocket is the admin socket the CLI talks to.
	DefaultAdminSocket = "/run/ssoossh-nss/admin.sock"
	// UserdbSocket is where systemd's socket unit holds the userdb socket.
	UserdbSocket = "/run/systemd/userdb/com.github.mnestor.ssoossh"
)

// DefaultSSHDUnits is the sshd_units default: the systemd units sshd runs
// as on the distributions measured. An entry ending in "@.service" is a
// template and matches every instance of it, which is how a socket
// activated sshd runs each connection. One function returning a fresh
// slice, not a package variable, so no caller can change the default for
// another.
func DefaultSSHDUnits() []string {
	return []string{"sshd.service", "ssh.service", "sshd@.service", "ssh@.service"}
}

// Userdb connection limits, socket_max_connections and
// socket_idle_timeout. Sized in docs/proposals/nss-directory/3-nss-daemon.md
// ("Connection limits"): nss-systemd holds one connection per lookup in
// flight and two during initgroups, so 1024 is far above what the busiest
// host keeps open at once, and at about 80 KiB a connection at worst it
// bounds the daemon's connection memory near 80 MiB.
const (
	// DefaultSocketMaxConnections is socket_max_connections' default.
	DefaultSocketMaxConnections = 1024
	// MinSocketMaxConnections keeps at least one connection for uid 0.
	MinSocketMaxConnections = 4
	// DefaultSocketIdleTimeout is socket_idle_timeout's default. A caller
	// sends its call as soon as it connects and closes after the answer,
	// so a connection is idle for milliseconds in all.
	DefaultSocketIdleTimeout = 10 * time.Second
	// MinSocketIdleTimeout is the read timeout each call is given to
	// arrive: a shorter budget would cut a caller still sending its call.
	MinSocketIdleTimeout = time.Second
)

// TLSCASystem is the tls_ca_file value that trusts the host's system
// trust store instead of a file. Exactly this spelling: any other case of
// it is refused, so a typo is never read as a file name or as the store.
const TLSCASystem = "system"

// Cache modes.
const (
	// CacheModePersistent keeps the cache in SQLite across restarts.
	CacheModePersistent = "persistent"
	// CacheModeMemory keeps it in process memory only.
	CacheModeMemory = "memory"
)

// Config is nss.yaml, decoded. Durations accept Go duration strings
// ("90m") and, as sssd.conf does, plain integers counted in seconds.
type Config struct {
	// Server is the ssoosshd https URL; required. Failover across several
	// servers is a load balancer's job behind this one URL.
	Server string `mapstructure:"server"`
	// TLSCAFile is a CA bundle the server's chain must end in, or
	// TLSCASystem for the host's system trust store; it or TLSSPKIPin is
	// required.
	TLSCAFile string `mapstructure:"tls_ca_file"`
	// TLSSPKIPin are base64 SHA-256 digests of the server's public key.
	TLSSPKIPin []string `mapstructure:"tls_spki_pin"`
	// APIKeyFile holds the fleet key: line 1 the key id, line 2 the secret.
	APIKeyFile string `mapstructure:"api_key_file"`
	// Timeout bounds one upstream lookup, retries included.
	Timeout time.Duration `mapstructure:"timeout"`
	// ReconnectionRetries is how many more attempts a lookup makes after a
	// transport failure before the daemon goes offline.
	ReconnectionRetries int `mapstructure:"reconnection_retries"`
	// OfflineTimeout is the first wait before probing an offline server;
	// it doubles on each failed probe.
	OfflineTimeout time.Duration `mapstructure:"offline_timeout"`
	// SocketAnswerDeadline is how long a userdb call may take to answer.
	SocketAnswerDeadline time.Duration `mapstructure:"socket_answer_deadline"`
	// SocketMaxConnections caps the userdb connections served at once; a
	// quarter of them only uid 0 peers may take. One over the cap is
	// answered ServiceNotAvailable and closed.
	SocketMaxConnections int `mapstructure:"socket_max_connections"`
	// SocketIdleTimeout is the total time a userdb connection may spend
	// with no call in progress, over its whole life. A call never resets
	// it, and a call in progress never counts against it.
	SocketIdleTimeout time.Duration `mapstructure:"socket_idle_timeout"`

	// CacheMode is CacheModePersistent or CacheModeMemory.
	CacheMode string `mapstructure:"cache_mode"`
	// EntryCacheTimeout is the TTL of a positive entry.
	EntryCacheTimeout time.Duration `mapstructure:"entry_cache_timeout"`
	// EntryCacheUserTimeout overrides it for users; 0 inherits.
	EntryCacheUserTimeout time.Duration `mapstructure:"entry_cache_user_timeout"`
	// EntryCacheGroupTimeout overrides it for groups; 0 inherits.
	EntryCacheGroupTimeout time.Duration `mapstructure:"entry_cache_group_timeout"`
	// EntryNegativeTimeout is how long an authoritative miss is remembered.
	EntryNegativeTimeout time.Duration `mapstructure:"entry_negative_timeout"`
	// EntryCacheNowaitPercentage is the share of the TTL during which an
	// entry is fresh; after it the entry is served and refreshed in the
	// background. 0 turns the background refresh off.
	EntryCacheNowaitPercentage int `mapstructure:"entry_cache_nowait_percentage"`
	// RefreshExpiredInterval is the background sweep's period. 0 means 75%
	// of the TTL in persistent mode and no sweep in memory mode.
	RefreshExpiredInterval time.Duration `mapstructure:"refresh_expired_interval"`
	// RefreshRate caps background upstream refreshes per second.
	RefreshRate float64 `mapstructure:"refresh_rate"`
	// CacheExpireOffline is how long after its fetch an entry may still be
	// served while the server is unreachable. 0 means for ever.
	CacheExpireOffline time.Duration `mapstructure:"cache_expire_offline"`

	// LoginSoftTTL is the age past which an sshd lookup refreshes the
	// entry first, and the RefreshUser debounce per name.
	LoginSoftTTL time.Duration `mapstructure:"login_soft_ttl"`
	// LoginRefreshTimeout bounds that synchronous refresh.
	LoginRefreshTimeout time.Duration `mapstructure:"login_refresh_timeout"`
	// SSHDUnits are the systemd units a uid 0 peer's cgroup must name to
	// count as sshd; "name@.service" matches any instance of the template.
	SSHDUnits []string `mapstructure:"sshd_units"`

	// MinID and MaxID bound every uid and gid served; MaxID 0 means none.
	MinID uint32 `mapstructure:"min_id"`
	MaxID uint32 `mapstructure:"max_id"`
	// OverrideGID replaces every user's primary gid; 0 means do not.
	OverrideGID uint32 `mapstructure:"override_gid"`
	// OverrideHomedir replaces every home directory; empty means do not.
	OverrideHomedir string `mapstructure:"override_homedir"`
	// FallbackHomedir is the home when the server sends none.
	FallbackHomedir string `mapstructure:"fallback_homedir"`
	// OverrideShell replaces every shell, if acceptable.
	OverrideShell string `mapstructure:"override_shell"`
	// ShellFallback is the shell when no other candidate is acceptable.
	ShellFallback string `mapstructure:"shell_fallback"`
	// AllowedShells are the acceptable shells; empty means /etc/shells.
	AllowedShells []string `mapstructure:"allowed_shells"`
	// VetoedShells are never acceptable.
	VetoedShells []string `mapstructure:"vetoed_shells"`
	// DisabledShell is the shell of a disabled or deleted account.
	DisabledShell string `mapstructure:"disabled_shell"`
	// IgnoreGroupMembers drops member lists from group answers.
	IgnoreGroupMembers bool `mapstructure:"ignore_group_members"`
	// FilterUsers and FilterGroups are names never served.
	FilterUsers  []string `mapstructure:"filter_users"`
	FilterGroups []string `mapstructure:"filter_groups"`
	// RequireGroups, when set, serves only members of at least one.
	RequireGroups []string `mapstructure:"require_groups"`
}

// Defaults returns the configuration a file that sets nothing gets: the
// values Part 3's example nss.yaml lists.
func Defaults() Config {
	return Config{
		APIKeyFile:                 DefaultKeyPath,
		Timeout:                    5 * time.Second,
		ReconnectionRetries:        3,
		OfflineTimeout:             60 * time.Second,
		SocketAnswerDeadline:       2500 * time.Millisecond,
		SocketMaxConnections:       DefaultSocketMaxConnections,
		SocketIdleTimeout:          DefaultSocketIdleTimeout,
		CacheMode:                  CacheModePersistent,
		EntryCacheTimeout:          5400 * time.Second,
		EntryNegativeTimeout:       15 * time.Second,
		EntryCacheNowaitPercentage: 50,
		RefreshRate:                2,
		LoginSoftTTL:               60 * time.Second,
		LoginRefreshTimeout:        time.Second,
		SSHDUnits:                  DefaultSSHDUnits(),
		MinID:                      1000,
		FallbackHomedir:            "/home/%u",
		ShellFallback:              "/bin/sh",
		DisabledShell:              "/usr/sbin/nologin",
		FilterUsers:                []string{"root"},
		FilterGroups:               []string{"root"},
	}
}

// Load reads the configuration file at path, which must be private to
// root (see ReadPrivateFile), and validates it. Keys the daemon does not
// know are an error: a misspelt option in a file that decides who may log
// in is better caught at start than silently ignored.
func Load(path string) (*Config, error) {
	return load(path, os.Geteuid())
}

// load is Load with the effective uid supplied, so a test can exercise the
// root-only owner check.
func load(path string, euid int) (*Config, error) {
	data, err := readPrivateFile(path, euid)
	if err != nil {
		return nil, err
	}

	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	c := Defaults()
	if err := v.UnmarshalExact(&c, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		// viper's own two hooks, which a custom hook replaces wholesale.
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
		secondsToDurationHook(),
	))); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// secondsToDurationHook decodes a bare number into a time.Duration as that
// many seconds, the way sssd.conf counts. Without it "entry_cache_timeout:
// 5400" would be 5400 nanoseconds.
func secondsToDurationHook() mapstructure.DecodeHookFuncType {
	return func(from, to reflect.Type, data any) (any, error) {
		if to != reflect.TypeFor[time.Duration]() {
			return data, nil
		}
		switch n := data.(type) {
		case int:
			return time.Duration(n) * time.Second, nil
		case float64:
			return time.Duration(n * float64(time.Second)), nil
		default:
			return data, nil
		}
	}
}

// TTLUser is the effective user TTL.
func (c *Config) TTLUser() time.Duration {
	if c.EntryCacheUserTimeout > 0 {
		return c.EntryCacheUserTimeout
	}
	return c.EntryCacheTimeout
}

// TTLGroup is the effective group TTL.
func (c *Config) TTLGroup() time.Duration {
	if c.EntryCacheGroupTimeout > 0 {
		return c.EntryCacheGroupTimeout
	}
	return c.EntryCacheTimeout
}

// SweepInterval is the background sweep's period, zero for none:
// refresh_expired_interval when set, else 75% of the shorter TTL in
// persistent mode and no sweep in memory mode.
func (c *Config) SweepInterval() time.Duration {
	if c.RefreshExpiredInterval > 0 {
		return c.RefreshExpiredInterval
	}
	if c.CacheMode != CacheModePersistent {
		return 0
	}
	return min(c.TTLUser(), c.TTLGroup()) * 3 / 4
}

// TLSSystemRoots reports whether tls_ca_file asks for the host's system
// trust store rather than naming a file.
func (c *Config) TLSSystemRoots() bool {
	return c.TLSCAFile == TLSCASystem
}

// InRange reports whether id lies within min_id..max_id.
func (c *Config) InRange(id uint32) bool {
	return id >= c.MinID && (c.MaxID == 0 || id <= c.MaxID)
}

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("invalid configuration")

// Validate checks every option against what the daemon can act on. Each
// failure names the option, so a refused start says what to fix.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...))
	}

	c.validateServer(add)
	c.validateTimings(add)
	c.validateIDs(add)
	c.validatePaths(add)
	return errors.Join(errs...)
}

// validateServer checks where the daemon connects and what it trusts.
func (c *Config) validateServer(add func(string, ...any)) {
	if c.Server == "" {
		add(`server is required: the ssoosshd https URL, e.g. "https://sso.example.com"`)
	} else if u, err := url.Parse(c.Server); err != nil || u.Scheme != "https" || u.Host == "" {
		add("server %q must be an https URL", c.Server)
	}
	if c.TLSCAFile == "" && len(c.TLSSPKIPin) == 0 {
		add("one of tls_ca_file and tls_spki_pin is required: set tls_ca_file to a CA bundle, or to %s to trust the host's system store", TLSCASystem)
	}
	if c.TLSCAFile != TLSCASystem && strings.EqualFold(strings.TrimSpace(c.TLSCAFile), TLSCASystem) {
		add("tls_ca_file %q is not a value the daemon knows: write %s, in lower case, to trust the host's system store", c.TLSCAFile, TLSCASystem)
	}
	if c.APIKeyFile == "" {
		add("api_key_file is required")
	}
}

// validateTimings checks the timeouts, TTLs and rates.
func (c *Config) validateTimings(add func(string, ...any)) {
	positive := map[string]time.Duration{
		"timeout":                c.Timeout,
		"offline_timeout":        c.OfflineTimeout,
		"socket_answer_deadline": c.SocketAnswerDeadline,
		"entry_cache_timeout":    c.EntryCacheTimeout,
		"entry_negative_timeout": c.EntryNegativeTimeout,
		"login_refresh_timeout":  c.LoginRefreshTimeout,
	}
	for _, name := range slices.Sorted(maps.Keys(positive)) {
		if positive[name] <= 0 {
			add("%s must be greater than zero", name)
		}
	}
	notNegative := map[string]time.Duration{
		"entry_cache_user_timeout":  c.EntryCacheUserTimeout,
		"entry_cache_group_timeout": c.EntryCacheGroupTimeout,
		"refresh_expired_interval":  c.RefreshExpiredInterval,
		"cache_expire_offline":      c.CacheExpireOffline,
		"login_soft_ttl":            c.LoginSoftTTL,
	}
	for _, name := range slices.Sorted(maps.Keys(notNegative)) {
		if notNegative[name] < 0 {
			add("%s must not be negative", name)
		}
	}
	// systemd's varlink default is 45 s and userdb sets nothing shorter,
	// so an answer later than that reaches no caller.
	if c.SocketAnswerDeadline >= 45*time.Second {
		add("socket_answer_deadline must be under 45s, systemd's own call timeout")
	}
	if c.LoginRefreshTimeout > c.SocketAnswerDeadline {
		add("login_refresh_timeout must not exceed socket_answer_deadline")
	}
	if c.SocketMaxConnections < MinSocketMaxConnections {
		add("socket_max_connections must be at least %d, so a quarter of it is kept for uid 0 callers", MinSocketMaxConnections)
	}
	if c.SocketIdleTimeout < MinSocketIdleTimeout {
		add("socket_idle_timeout must be at least %v, the time each call is given to arrive", MinSocketIdleTimeout)
	}
	if c.ReconnectionRetries < 0 {
		add("reconnection_retries must not be negative")
	}
	if c.EntryCacheNowaitPercentage < 0 || c.EntryCacheNowaitPercentage > 100 {
		add("entry_cache_nowait_percentage must be between 0 and 100")
	}
	if c.RefreshRate <= 0 {
		add("refresh_rate must be greater than zero")
	}
	if c.CacheMode != CacheModePersistent && c.CacheMode != CacheModeMemory {
		add("cache_mode %q must be %s or %s", c.CacheMode, CacheModePersistent, CacheModeMemory)
	}
}

// validateIDs checks the id range and the names lists.
func (c *Config) validateIDs(add func(string, ...any)) {
	// min_id 0 would let the directory serve uid 0.
	if c.MinID == 0 {
		add("min_id must be at least 1")
	}
	if c.MaxID != 0 && c.MaxID < c.MinID {
		add("max_id must be 0 or at least min_id")
	}
	lists := map[string][]string{
		"filter_users":   c.FilterUsers,
		"filter_groups":  c.FilterGroups,
		"require_groups": c.RequireGroups,
	}
	for _, name := range slices.Sorted(maps.Keys(lists)) {
		for _, entry := range lists[name] {
			if err := posixname.Valid(entry); err != nil {
				add("%s: %v", name, err)
			}
		}
	}
}

// validatePaths checks every path option is absolute and safe in passwd.
func (c *Config) validatePaths(add func(string, ...any)) {
	single := map[string]string{
		"shell_fallback":   c.ShellFallback,
		"disabled_shell":   c.DisabledShell,
		"override_shell":   c.OverrideShell,
		"fallback_homedir": c.FallbackHomedir,
		"override_homedir": c.OverrideHomedir,
	}
	required := []string{"shell_fallback", "disabled_shell", "fallback_homedir"}
	for _, name := range slices.Sorted(maps.Keys(single)) {
		value := single[name]
		switch {
		case value == "" && slices.Contains(required, name):
			add("%s is required", name)
		case value == "":
		case strings.HasSuffix(name, "_homedir") && strings.HasPrefix(value, "%h"):
			// "%h/..." starts with the server's home, which the policy
			// checks is absolute after expansion.
		case !ValidPath(value):
			add("%s %q must be an absolute path with no ':' or control character", name, value)
		}
	}
	lists := map[string][]string{
		"allowed_shells": c.AllowedShells,
		"vetoed_shells":  c.VetoedShells,
	}
	for _, name := range slices.Sorted(maps.Keys(lists)) {
		for _, p := range lists[name] {
			if !ValidPath(p) {
				add("%s entry %q must be an absolute path", name, p)
			}
		}
	}
	for _, unit := range c.SSHDUnits {
		if !validUnitName(unit) {
			add("sshd_units entry %q must be a unit name ending in .service", unit)
		}
	}
}

// validUnitName reports whether unit can name a service a cgroup path
// ends in: "x.service" or the template "x@.service", with no path
// separator.
func validUnitName(unit string) bool {
	stem, ok := strings.CutSuffix(unit, ".service")
	return ok && stem != "" && stem != "@" && !strings.Contains(unit, "/") &&
		!strings.ContainsFunc(unit, func(r rune) bool { return r <= ' ' || r == 0x7f })
}

// ValidPath reports whether p can be a passwd home or shell: absolute,
// already clean, and free of ':' and control characters, which would
// corrupt the line getent prints.
func ValidPath(p string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Contains(p, ":") {
		return false
	}
	return !strings.ContainsFunc(p, func(r rune) bool { return r < ' ' || r == 0x7f })
}
