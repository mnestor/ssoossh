package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// minimalYAML is the smallest valid nss.yaml: a server and a pin.
const minimalYAML = "server: https://sso.example.com\ntls_spki_pin: [AAAA]\n"

// writeFile writes content to name in dir with mode and returns its path.
func writeFile(t *testing.T, dir, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// nonRootUID is the owner given to a file that must not be root's when the
// test itself runs as root: nobody on Linux.
const nonRootUID = 65534

// ownByNonRoot makes path owned by someone other than root. A test run as
// root (as CI does) chowns it to nonRootUID; any other run already owns it
// as the non-root test user.
func ownByNonRoot(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() != 0 {
		return
	}
	if err := os.Chown(path, nonRootUID, nonRootUID); err != nil {
		t.Fatal(err)
	}
}

func TestLoadShouldApplyDefaultsWhenTheFileSetsOnlyTheServer(t *testing.T) {
	t.Parallel()
	path := writeFile(t, t.TempDir(), "nss.yaml", minimalYAML, 0o600)

	c, err := load(path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	if c.Timeout != want.Timeout || c.EntryCacheTimeout != 90*time.Minute || c.MinID != 1000 ||
		c.CacheMode != CacheModePersistent || !slices.Equal(c.FilterUsers, []string{"root"}) ||
		c.APIKeyFile != DefaultKeyPath || c.SocketAnswerDeadline != 2500*time.Millisecond ||
		c.SocketMaxConnections != 1024 || c.SocketIdleTimeout != 10*time.Second {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestLoadShouldDecodeEveryOptionWhenTheFileSetsThem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yaml := `server: https://sso.example.com:8443
tls_ca_file: /etc/ssoossh/ca.pem
tls_spki_pin: "AAAA,BBBB"
api_key_file: /etc/ssoossh/other.key
timeout: 3s
reconnection_retries: 1
offline_timeout: 30
socket_answer_deadline: 2s
socket_max_connections: 256
socket_idle_timeout: 30
cache_mode: memory
entry_cache_timeout: 600
entry_cache_user_timeout: 300s
entry_cache_group_timeout: 1.5
entry_negative_timeout: 5s
entry_cache_nowait_percentage: 0
refresh_expired_interval: 1m
refresh_rate: 0.5
cache_expire_offline: 24h
login_soft_ttl: 10s
login_refresh_timeout: 500ms
sshd_units: [sshd.service]
min_id: 2000
max_id: 60000
override_gid: 100
override_homedir: /srv/%u
fallback_homedir: /home/users/%u
override_shell: /bin/bash
shell_fallback: /bin/bash
allowed_shells: [/bin/bash, /bin/zsh]
vetoed_shells: [/bin/csh]
disabled_shell: /bin/false
ignore_group_members: true
filter_users: [root, admin]
filter_groups: [wheel]
require_groups: [staff]
`
	path := writeFile(t, dir, "nss.yaml", yaml, 0o600)

	c, err := load(path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"server", c.Server == "https://sso.example.com:8443"},
		{"homes", c.OverrideHomedir == "/srv/%u" && c.FallbackHomedir == "/home/users/%u"},
		{"comma-separated pins", slices.Equal(c.TLSSPKIPin, []string{"AAAA", "BBBB"})},
		{"bare seconds", c.OfflineTimeout == 30*time.Second && c.EntryCacheTimeout == 10*time.Minute},
		{"fractional seconds", c.EntryCacheGroupTimeout == 1500*time.Millisecond},
		{"duration strings", c.Timeout == 3*time.Second && c.LoginRefreshTimeout == 500*time.Millisecond},
		{"float rate", c.RefreshRate == 0.5},
		{"ids", c.MinID == 2000 && c.MaxID == 60000 && c.OverrideGID == 100},
		{"shell lists", slices.Equal(c.AllowedShells, []string{"/bin/bash", "/bin/zsh"}) && slices.Equal(c.VetoedShells, []string{"/bin/csh"})},
		{"lists", slices.Equal(c.FilterUsers, []string{"root", "admin"}) && slices.Equal(c.RequireGroups, []string{"staff"})},
		{"bools", c.IgnoreGroupMembers},
		{"user ttl", c.TTLUser() == 5*time.Minute},
		{"group ttl", c.TTLGroup() == 1500*time.Millisecond},
		{"explicit sweep", c.SweepInterval() == time.Minute},
		{"sshd units", slices.Equal(c.SSHDUnits, []string{"sshd.service"})},
		{"connection limits", c.SocketMaxConnections == 256 && c.SocketIdleTimeout == 30*time.Second},
	}
	for _, check := range checks {
		if !check.ok {
			t.Errorf("should decode %s when the file sets it: %+v", check.name, c)
		}
	}
}

func TestLoadShouldRefuseTheFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		euid    int
		// foreignOwner gives the file a non-root owner.
		foreignOwner bool
		wantErr      string
	}{
		{name: "should refuse a group-readable file when it may hold secrets", content: minimalYAML, mode: 0o640, euid: os.Geteuid(), wantErr: "group- or world-accessible"},
		{name: "should refuse a world-readable file when it may hold secrets", content: minimalYAML, mode: 0o604, euid: os.Geteuid(), wantErr: "group- or world-accessible"},
		{name: "should refuse a file not owned by root when running as root", content: minimalYAML, mode: 0o600, euid: 0, foreignOwner: true, wantErr: "not root"},
		{name: "should refuse an unknown key when it is probably a typo", content: minimalYAML + "entry_cache_timout: 5s\n", mode: 0o600, euid: os.Geteuid(), wantErr: "entry_cache_timout"},
		{name: "should refuse dns_discovery_domain when it is no longer an option", content: minimalYAML + "dns_discovery_domain: example.com\n", mode: 0o600, euid: os.Geteuid(), wantErr: "dns_discovery_domain"},
		{name: "should refuse a file with no server when server is required", content: "tls_spki_pin: [AAAA]\n", mode: 0o600, euid: os.Geteuid(), wantErr: "server is required"},
		{name: "should refuse invalid YAML when it cannot be parsed", content: "server: [\n", mode: 0o600, euid: os.Geteuid(), wantErr: "parse"},
		{name: "should refuse a wrong type when a duration is a list", content: minimalYAML + "timeout: [1]\n", mode: 0o600, euid: os.Geteuid(), wantErr: "timeout"},
		{name: "should refuse an invalid value when validation fails", content: minimalYAML + "min_id: 0\n", mode: 0o600, euid: os.Geteuid(), wantErr: "min_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeFile(t, t.TempDir(), "nss.yaml", tt.content, tt.mode)
			if tt.foreignOwner {
				ownByNonRoot(t, path)
			}
			_, err := load(path, tt.euid)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadShouldFailWhenTheFileIsMissing(t *testing.T) {
	t.Parallel()
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want ErrNotExist", err)
	}
}

func TestLoadShouldRefuseADirectoryWhenGivenOneAsTheFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, ErrInsecureFile) {
		t.Fatalf("got %v, want ErrInsecureFile", err)
	}
}

func TestLoadShouldRefuseAnOversizeFileWhenItPassesTheCap(t *testing.T) {
	t.Parallel()
	big := minimalYAML + "#" + strings.Repeat("x", maxConfigFileBytes) + "\n"
	path := writeFile(t, t.TempDir(), "nss.yaml", big, 0o600)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("got %v, want a size error", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	valid := func() Config {
		c := Defaults()
		c.Server = "https://sso.example.com"
		c.TLSSPKIPin = []string{"AAAA"}
		return c
	}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "should accept the defaults when a server and a pin are set", mutate: func(*Config) {}},
		{name: "should accept a %h home when it starts from the server's", mutate: func(c *Config) { c.OverrideHomedir = "%h/x" }},
		{name: "should refuse http when only https is pinned", mutate: func(c *Config) { c.Server = "http://sso.example.com" }, wantErr: "https URL"},
		{name: "should refuse a server with no host when the URL is empty of one", mutate: func(c *Config) { c.Server = "https://" }, wantErr: "https URL"},
		{name: "should refuse an unparsable server when the URL is malformed", mutate: func(c *Config) { c.Server = "https://a b\x7f" }, wantErr: "https URL"},
		{name: "should refuse an empty server with an example when server is required", mutate: func(c *Config) { c.Server = "" }, wantErr: `server is required: the ssoosshd https URL, e.g. "https://sso.example.com"`},
		{name: "should refuse an empty server when only a CA file is set", mutate: func(c *Config) {
			c.Server, c.TLSSPKIPin, c.TLSCAFile = "", nil, "/etc/ssoossh/ca.pem"
		}, wantErr: "server is required"},
		{name: "should refuse a bare host name when it is not a URL", mutate: func(c *Config) { c.Server = "sso.example.com" }, wantErr: "https URL"},
		{name: "should refuse no trust root when neither CA nor pin is set", mutate: func(c *Config) { c.TLSSPKIPin = nil }, wantErr: "tls_ca_file"},
		{name: "should accept a CA file when no pin is set", mutate: func(c *Config) { c.TLSSPKIPin = nil; c.TLSCAFile = "/etc/ssoossh/ca.pem" }},
		{name: "should accept the system store when no pin is set", mutate: func(c *Config) { c.TLSSPKIPin = nil; c.TLSCAFile = "system" }},
		{name: "should accept the system store when pins are set as well", mutate: func(c *Config) { c.TLSCAFile = "system" }},
		{name: "should refuse System with the suggestion when it is not lower case", mutate: func(c *Config) { c.TLSCAFile = "System" }, wantErr: `tls_ca_file "System" is not a value the daemon knows: write system, in lower case`},
		{name: "should refuse SYSTEM with the suggestion when it is upper case", mutate: func(c *Config) { c.TLSCAFile = "SYSTEM" }, wantErr: "write system"},
		{name: "should refuse a padded system with the suggestion when it has spaces", mutate: func(c *Config) { c.TLSCAFile = " system " }, wantErr: "write system"},
		{name: "should refuse no key file when it is empty", mutate: func(c *Config) { c.APIKeyFile = "" }, wantErr: "api_key_file"},
		{name: "should refuse a zero timeout when it must be positive", mutate: func(c *Config) { c.Timeout = 0 }, wantErr: "timeout must be greater"},
		{name: "should refuse a negative soft TTL when it must not be negative", mutate: func(c *Config) { c.LoginSoftTTL = -1 }, wantErr: "login_soft_ttl"},
		{name: "should refuse a 45s deadline when systemd gives up then", mutate: func(c *Config) { c.SocketAnswerDeadline = 45 * time.Second }, wantErr: "under 45s"},
		{name: "should refuse a login refresh longer than the deadline when it could never finish", mutate: func(c *Config) { c.LoginRefreshTimeout = 3 * time.Second }, wantErr: "login_refresh_timeout must not exceed"},
		{name: "should accept the smallest cap when a slot is still kept for uid 0", mutate: func(c *Config) { c.SocketMaxConnections = 4 }},
		{name: "should refuse a cap under 4 when no slot would be kept for uid 0", mutate: func(c *Config) { c.SocketMaxConnections = 3 }, wantErr: "socket_max_connections must be at least 4"},
		{name: "should refuse a zero cap when no connection could be served", mutate: func(c *Config) { c.SocketMaxConnections = 0 }, wantErr: "socket_max_connections"},
		{name: "should accept a 1s idle timeout when it equals the read timeout", mutate: func(c *Config) { c.SocketIdleTimeout = time.Second }},
		{name: "should refuse an idle timeout under 1s when it could cut a call still arriving", mutate: func(c *Config) { c.SocketIdleTimeout = 999 * time.Millisecond }, wantErr: "socket_idle_timeout must be at least 1s"},
		{name: "should refuse a negative idle timeout when it must be at least 1s", mutate: func(c *Config) { c.SocketIdleTimeout = -1 }, wantErr: "socket_idle_timeout"},
		{name: "should refuse negative retries when they must not be negative", mutate: func(c *Config) { c.ReconnectionRetries = -1 }, wantErr: "reconnection_retries"},
		{name: "should refuse a nowait over 100 when it is a percentage", mutate: func(c *Config) { c.EntryCacheNowaitPercentage = 101 }, wantErr: "nowait"},
		{name: "should refuse a zero refresh rate when it must be positive", mutate: func(c *Config) { c.RefreshRate = 0 }, wantErr: "refresh_rate"},
		{name: "should refuse an unknown cache mode when it is neither", mutate: func(c *Config) { c.CacheMode = "disk" }, wantErr: "cache_mode"},
		{name: "should refuse min_id 0 when it would serve root", mutate: func(c *Config) { c.MinID = 0 }, wantErr: "min_id"},
		{name: "should refuse max_id below min_id when the range is empty", mutate: func(c *Config) { c.MaxID = 10 }, wantErr: "max_id"},
		{name: "should refuse an invalid filter name when it can never match", mutate: func(c *Config) { c.FilterUsers = []string{"a:b"} }, wantErr: "filter_users"},
		{name: "should refuse an empty fallback shell when it is required", mutate: func(c *Config) { c.ShellFallback = "" }, wantErr: "shell_fallback is required"},
		{name: "should refuse a relative disabled shell when it must be absolute", mutate: func(c *Config) { c.DisabledShell = "nologin" }, wantErr: "disabled_shell"},
		{name: "should refuse a relative allowed shell when it must be absolute", mutate: func(c *Config) { c.AllowedShells = []string{"bash"} }, wantErr: "allowed_shells"},
		{name: "should accept a template unit when it ends in @.service", mutate: func(c *Config) { c.SSHDUnits = []string{"sshd@.service"} }},
		{name: "should refuse a unit that is not a service when it ends otherwise", mutate: func(c *Config) { c.SSHDUnits = []string{"sshd.socket"} }, wantErr: "sshd_units"},
		{name: "should refuse a bare template when it has no name", mutate: func(c *Config) { c.SSHDUnits = []string{"@.service"} }, wantErr: "sshd_units"},
		{name: "should refuse a unit with a slash when it is a path", mutate: func(c *Config) { c.SSHDUnits = []string{"system.slice/sshd.service"} }, wantErr: "sshd_units"},
		{name: "should refuse a unit with a space when it cannot be a unit name", mutate: func(c *Config) { c.SSHDUnits = []string{"ss hd.service"} }, wantErr: "sshd_units"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := valid()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want an ErrInvalid containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestTLSSystemRoots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		caFile string
		want   bool
	}{
		{name: "should trust the system store when tls_ca_file is system", caFile: "system", want: true},
		{name: "should not when tls_ca_file is a path", caFile: "/etc/ssl/certs/ca-certificates.crt"},
		{name: "should not when tls_ca_file is empty", caFile: ""},
		{name: "should not when the spelling differs", caFile: "System"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Config{TLSCAFile: tt.caFile}
			if got := c.TLSSystemRoots(); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadShouldReadTheSystemStoreWhenTheFileSaysSystem(t *testing.T) {
	t.Parallel()
	path := writeFile(t, t.TempDir(), "nss.yaml", "server: https://sso.example.com\ntls_ca_file: system\n", 0o600)

	c, err := load(path, os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	if !c.TLSSystemRoots() {
		t.Fatalf("got tls_ca_file %q, want the system store", c.TLSCAFile)
	}
}

func TestSweepInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode string
		set  time.Duration
		want time.Duration
	}{
		{name: "should sweep at 75% of the TTL when persistent and unset", mode: CacheModePersistent, want: 5400 * time.Second * 3 / 4},
		{name: "should not sweep when in memory and unset", mode: CacheModeMemory, want: 0},
		{name: "should use the setting when one is given", mode: CacheModeMemory, set: time.Minute, want: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Defaults()
			c.CacheMode, c.RefreshExpiredInterval = tt.mode, tt.set
			if got := c.SweepInterval(); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		min, max uint32
		id       uint32
		want     bool
	}{
		{name: "should accept min_id when it is the floor", min: 1000, id: 1000, want: true},
		{name: "should refuse below min_id when under the floor", min: 1000, id: 999},
		{name: "should accept any high id when max_id is 0", min: 1000, id: 1 << 31, want: true},
		{name: "should accept max_id when it is the ceiling", min: 1000, max: 2000, id: 2000, want: true},
		{name: "should refuse above max_id when over the ceiling", min: 1000, max: 2000, id: 2001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Config{MinID: tt.min, MaxID: tt.max}
			if got := c.InRange(tt.id); got != tt.want {
				t.Fatalf("InRange(%d) = %v", tt.id, got)
			}
		})
	}
}

func TestValidPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "should accept an absolute clean path when it is plain", path: "/home/alice", want: true},
		{name: "should refuse a relative path when it is not absolute", path: "home/alice"},
		{name: "should refuse an unclean path when it has dot segments", path: "/home/../root"},
		{name: "should refuse a colon when it would split passwd", path: "/home/a:b"},
		{name: "should refuse a newline when it would split records", path: "/home/a\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidPath(tt.path); got != tt.want {
				t.Fatalf("ValidPath(%q) = %v", tt.path, got)
			}
		})
	}
}

func TestDefaultSSHDUnitsShouldReturnAFreshSliceWhenCalledTwice(t *testing.T) {
	t.Parallel()
	a := DefaultSSHDUnits()
	a[0] = "evil.service"
	if got := DefaultSSHDUnits(); !slices.Equal(got, []string{"sshd.service", "ssh.service", "sshd@.service", "ssh@.service"}) {
		t.Fatalf("a caller changed the default for everyone: %v", got)
	}
}
