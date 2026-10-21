package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/internal/configcheck"
)

// startupFiles is one host's nss.yaml, override file and fleet key.
type startupFiles struct {
	config, overrides, key string
}

// writeStartupFiles writes a valid set of the three files, each with mode,
// nss.yaml naming the key file and adding extra.
func writeStartupFiles(t *testing.T, extra string, configMode, overridesMode, keyMode os.FileMode) startupFiles {
	t.Helper()
	dir := t.TempDir()
	key := writeFile(t, dir, "nss.key", "nss_test\ns3cret-value\n", keyMode)
	return startupFiles{
		config:    writeFile(t, dir, "nss.yaml", minimalYAML+"api_key_file: "+key+"\n"+extra, configMode),
		overrides: writeFile(t, dir, "ovr.yaml", "", overridesMode),
		key:       key,
	}
}

// TestLoadStartup_ShouldReportEveryFileThatFails covers the start and
// `config check` hearing about every bad file at once, each named.
func TestLoadStartup_ShouldReportEveryFileThatFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		extra         string
		configMode    os.FileMode
		overridesMode os.FileMode
		keyMode       os.FileMode
		euid          int
		foreignOwner  bool
		wantFiles     func(f startupFiles) []string
		wantConfig    bool
	}{
		{
			name:       "should return no error when every file is fine",
			configMode: 0o600, overridesMode: 0o644, keyMode: 0o600, euid: os.Geteuid(),
			wantFiles:  func(startupFiles) []string { return nil },
			wantConfig: true,
		},
		{
			name:       "should name nss.yaml when it is group-readable",
			configMode: 0o640, overridesMode: 0o644, keyMode: 0o600, euid: os.Geteuid(),
			wantFiles: func(f startupFiles) []string { return []string{f.config} },
		},
		{
			name:       "should name nss.yaml and the override file when both have a bad mode, leaving the key unread",
			configMode: 0o604, overridesMode: 0o664, keyMode: 0o640, euid: os.Geteuid(),
			wantFiles: func(f startupFiles) []string { return []string{f.config, f.overrides} },
		},
		{
			name:       "should still check the key when nss.yaml decodes but is invalid",
			extra:      "min_id: 0\n",
			configMode: 0o600, overridesMode: 0o644, keyMode: 0o644, euid: os.Geteuid(),
			wantFiles:  func(f startupFiles) []string { return []string{f.config, f.key} },
			wantConfig: true,
		},
		{
			name:       "should name nss.yaml and the override file when root does not own them and the check runs as root",
			configMode: 0o600, overridesMode: 0o644, keyMode: 0o600, euid: 0, foreignOwner: true,
			wantFiles: func(f startupFiles) []string { return []string{f.config, f.overrides} },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := writeStartupFiles(t, tt.extra, tt.configMode, tt.overridesMode, tt.keyMode)
			if tt.foreignOwner {
				for _, p := range []string{f.config, f.overrides, f.key} {
					ownByNonRoot(t, p)
				}
			}
			cfg, err := loadStartup(f.config, f.overrides, tt.euid)

			var got []string
			for _, e := range configcheck.Split(err) {
				var fe *FileError
				if !errors.As(e, &fe) {
					t.Fatalf("%v is not a *FileError", e)
				}
				got = append(got, fe.File)
			}
			if want := tt.wantFiles(f); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("failing files = %v, want %v (err %v)", got, want, err)
			}
			if (cfg != nil) != tt.wantConfig {
				t.Errorf("config returned = %v, want %v", cfg != nil, tt.wantConfig)
			}
		})
	}
}

// TestLoadStartup_ShouldMarkAModeProblemAsAnInsecureFile covers the
// sentinel the check sorts file-permission findings by.
func TestLoadStartup_ShouldMarkAModeProblemAsAnInsecureFile(t *testing.T) {
	t.Parallel()
	f := writeStartupFiles(t, "", 0o644, 0o644, 0o600)
	_, err := loadStartup(f.config, f.overrides, os.Geteuid())
	if !errors.Is(err, ErrInsecureFile) {
		t.Errorf("err = %v, want ErrInsecureFile", err)
	}
}

// TestLoadStartup_ShouldNeverDiscloseTheKeySecret covers a broken key file:
// the error counts its lines and never quotes them.
func TestLoadStartup_ShouldNeverDiscloseTheKeySecret(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key := writeFile(t, dir, "nss.key", "nss_test\ns3cret-value\nstray\n", 0o600)
	config := writeFile(t, dir, "nss.yaml", minimalYAML+"api_key_file: "+key+"\n", 0o600)
	_, err := loadStartup(config, dir+"/absent-overrides.yaml", os.Geteuid())
	if err == nil {
		t.Fatal("loadStartup accepted a three-line key file")
	}
	if strings.Contains(err.Error(), "s3cret-value") {
		t.Errorf("error discloses the secret: %v", err)
	}
}

// TestConfig_Warnings_ShouldWarnWhenOnlyTheSystemStoreIsTrusted covers the
// start-time warning the check reports.
func TestConfig_Warnings_ShouldWarnWhenOnlyTheSystemStoreIsTrusted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  Config
		want int
	}{
		{name: "should warn when tls_ca_file is system and nothing is pinned", cfg: Config{TLSCAFile: TLSCASystem}, want: 1},
		{name: "should not warn when a pin decides the key", cfg: Config{TLSCAFile: TLSCASystem, TLSSPKIPin: []string{"AAAA"}}},
		{name: "should not warn when a CA file is named", cfg: Config{TLSCAFile: "/etc/ssoossh/ca.pem"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := len(tt.cfg.Warnings()); got != tt.want {
				t.Errorf("len(Warnings()) = %d, want %d", got, tt.want)
			}
		})
	}
}
