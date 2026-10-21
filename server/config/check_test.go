package config

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/mnestor/ssoossh/internal/configcheck"
)

// loadFile runs Load over yaml written to a temporary file.
func loadFile(t *testing.T, yaml string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssoosshd.yaml")
	writeFile(t, path, yaml)
	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("set --config: %v", err)
	}
	c, err := Load(cc)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

// sectionsOf lists the section of every error Validate returned.
func sectionsOf(err error) []string {
	var out []string
	for _, e := range configcheck.Split(err) {
		var se *SectionError
		if errors.As(e, &se) {
			out = append(out, se.Section)
		} else {
			out = append(out, "?")
		}
	}
	return out
}

// TestConfig_Validate_ShouldReportEveryFailingSection covers the start
// naming every block's problem at once instead of the first.
func TestConfig_Validate_ShouldReportEveryFailingSection(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{name: "should return nil when every block is valid", yaml: ""},
		{
			name: "should name one section when one block fails",
			yaml: "http:\n  public_url: https://ssh.example.com/sub\n",
			want: []string{"http"},
		},
		{
			name: "should name both sections when two blocks fail",
			yaml: "http:\n  public_url: https://ssh.example.com/sub\ncert_options:\n  client_timeout: 0s\n",
			want: []string{"http", "cert_options"},
		},
		{
			name: "should put a broker failure under pubsub when the nats block is incomplete",
			yaml: "pubsub:\n  backend: nats\n",
			want: []string{"pubsub"},
		},
		{
			name: "should put a root key failure under server when multi_instance has no cookie key",
			yaml: "multi_instance: true\n",
			want: []string{"server"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sectionsOf(loadFile(t, tt.yaml).Validate())
			if len(got) != len(tt.want) {
				t.Fatalf("sections = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("sections = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestNewConfig_ShouldRefuseWithEveryReasonWhenTwoBlocksFail covers the
// start's outcome: still a refusal, now naming both problems.
func TestNewConfig_ShouldRefuseWithEveryReasonWhenTwoBlocksFail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssoosshd.yaml")
	writeFile(t, path, "http:\n  public_url: https://ssh.example.com/sub\ncert_options:\n  client_timeout: 0s\n")
	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("set --config: %v", err)
	}
	_, err := NewConfig(cc)
	if got := len(configcheck.Split(err)); got != 2 {
		t.Fatalf("NewConfig error holds %d problems, want 2: %v", got, err)
	}
}

// TestConfig_ConfigFile_ShouldNameTheFileLoadRead covers the path the
// check reports.
func TestConfig_ConfigFile_ShouldNameTheFileLoadRead(t *testing.T) {
	c := loadFile(t, "")
	if filepath.Base(c.ConfigFile()) != "ssoosshd.yaml" {
		t.Errorf("ConfigFile() = %q, want the temporary ssoosshd.yaml", c.ConfigFile())
	}
}

// TestConfig_Warnings_ShouldTagEachWarningWithItsSection covers the
// start-time advice the check reports.
func TestConfig_Warnings_ShouldTagEachWarningWithItsSection(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{name: "should warn under http when public_url is not https", yaml: "http:\n  public_url: http://ssh.example.com\n", want: []string{"http"}},
		{name: "should not warn when public_url is https", yaml: "http:\n  public_url: https://ssh.example.com\n"},
		{
			name: "should warn under mail when the relay is remote and plaintext",
			yaml: "http:\n  public_url: https://ssh.example.com\nmail:\n  enabled: true\n  from: ssoossh@example.com\n  smtp:\n    host: smtp.example.com\n    tls: required\n    auth: none\n",
			want: []string{"mail"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := loadFile(t, tt.yaml)
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got := c.Warnings()
			if len(got) != len(tt.want) {
				t.Fatalf("Warnings() = %+v, want sections %v", got, tt.want)
			}
			for i := range got {
				if got[i].Section != tt.want[i] {
					t.Errorf("warning %d section = %q, want %q", i, got[i].Section, tt.want[i])
				}
			}
		})
	}
}

// TestConfig_EffectiveSources_ShouldNameTheLayerThatSetEachKey covers the
// value-origin view.
func TestConfig_EffectiveSources_ShouldNameTheLayerThatSetEachKey(t *testing.T) {
	c := loadFile(t, `
http:
  public_url: https://ssh.example.com
  cookie_key: a-cookie-secret
ldap:
  enabled: true
  schema: active-directory
  url: ldaps://ldap.example.com
  base_dn: dc=example,dc=com
  user_filter: (sAMAccountName={{.Username}})
cert_options:
  user:
    lifetime_policy:
      default_duration: 1h
      tiers:
        - when: {group: admins}
          max_duration: 2h
`)
	sources, err := c.EffectiveSources()
	if err != nil {
		t.Fatalf("EffectiveSources: %v", err)
	}
	byKey := map[string]SourcedSetting{}
	for _, s := range sources {
		byKey[s.Key] = s
	}

	tests := []struct {
		name string
		key  string
		want Origin
	}{
		{name: "should say file when the file sets the key", key: "http.public_url", want: OriginFile},
		{name: "should say preset when only the schema preset sets the key", key: "ldap.id_attribute", want: OriginPreset},
		{name: "should say default when no layer above defaults sets the key", key: "http.port", want: OriginDefault},
		{name: "should attribute a list element to the layer that set the list", key: "cert_options.user.lifetime_policy.tiers[0].max_duration", want: OriginFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := byKey[tt.key]
			if !ok {
				t.Fatalf("no setting %q", tt.key)
			}
			if s.Origin != tt.want {
				t.Errorf("%s origin = %q, want %q", tt.key, s.Origin, tt.want)
			}
		})
	}

	t.Run("should redact a secret the file sets", func(t *testing.T) {
		if got := byKey["http.cookie_key"].Value; got != Redacted {
			t.Errorf("http.cookie_key = %q, want %q", got, Redacted)
		}
	})
}

// TestConfig_EffectiveSources_ShouldFailWhenTheFileIsGone covers the
// re-read failing: the view says so rather than claiming defaults.
func TestConfig_EffectiveSources_ShouldFailWhenTheFileIsGone(t *testing.T) {
	c := &Config{file: filepath.Join(t.TempDir(), "gone.yaml")}
	if _, err := c.EffectiveSources(); err == nil {
		t.Error("EffectiveSources() returned no error for a missing file")
	}
}
