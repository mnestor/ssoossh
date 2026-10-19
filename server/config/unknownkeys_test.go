package config

// Test methodology: every case loads a real file through NewConfig, so the
// unknown keys come from the decoder's own metadata exactly as at startup.
// The warning text is checked on a logger of the test's own, never on the
// process-wide default.

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// should report each key nothing declares, with a suggestion when a known
// key is one slip away, and never fail the start.
func TestNewConfig_ShouldFindUnknownKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want []UnknownKey
	}{
		{
			name: "should report an unknown top-level key",
			file: "brandnew: true\n",
			want: []UnknownKey{{Key: "brandnew"}},
		},
		{
			name: "should report an unknown nested key",
			file: "ldap:\n  frobnicate: 3\n",
			want: []UnknownKey{{Key: "ldap.frobnicate"}},
		},
		{
			name: "should suggest the key a transposition away",
			file: "ldap:\n  schmea: lldap\n",
			want: []UnknownKey{{Key: "ldap.schmea", Suggestion: "ldap.schema"}},
		},
		{
			name: "should suggest a top-level section for a misspelt one",
			file: "nsss:\n  enabled: true\n",
			want: []UnknownKey{{Key: "nsss", Suggestion: "nss"}},
		},
		{
			name: "should suggest the snake_case key for a camelCase one",
			file: "ldap:\n  idAttribute: entryUUID\n",
			want: []UnknownKey{{Key: "ldap.idattribute", Suggestion: "ldap.id_attribute"}},
		},
		{
			name: "should report a misspelt field inside a map value",
			file: "ldap:\n  fields:\n    groups:\n      atribute: memberOf\n",
			want: []UnknownKey{{Key: "ldap.fields.groups.atribute", Suggestion: "ldap.fields.groups.attribute"}},
		},
		{
			name: "should report a misspelt field inside a list element",
			file: "ldap:\n  fields:\n    other_accounts:\n      searches:\n        - name: a\n          filter: (uid=x)\n          valeu: uid\n",
			want: []UnknownKey{{Key: "ldap.fields.other_accounts.searches[0].valeu", Suggestion: "ldap.fields.other_accounts.searches[0].value"}},
		},
		{
			name: "should not suggest a key that is not close",
			file: "http:\n  zzzzzz: 1\n",
			want: []UnknownKey{{Key: "http.zzzzzz"}},
		},
		{
			name: "should never report the operator's own map keys",
			file: "ldap:\n  fields:\n    department: departmentNumber\n    cost_centre:\n      attribute: costCenter\nauthentication:\n  fields:\n    extra:\n      anything_at_all: claim\n",
			want: nil,
		},
		{
			name: "should report nothing for a file of known keys",
			file: "http:\n  port: 9443\n",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := loadNSSConfig(t, "ssh_key: \"test-key-material\"\n"+tt.file)
			if err != nil {
				t.Fatalf("NewConfig() error = %v, want a warning and no failure", err)
			}
			got := c.UnknownKeys()
			if len(got) != len(tt.want) {
				t.Fatalf("UnknownKeys() = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("UnknownKeys()[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// should find nothing in the shipped defaults or in any preset, since all
// of them are layered under every file the server reads.
func TestNewConfig_ShouldFindNoUnknownKeysInDefaultsOrPresets(t *testing.T) {
	t.Parallel()

	files := []string{"ssh_key: \"test-key-material\"\n"}
	for _, schema := range LDAPSchemas() {
		files = append(files, "ssh_key: \"test-key-material\"\nldap:\n  schema: "+schema+"\n")
	}
	for _, file := range files {
		c, err := loadNSSConfig(t, file)
		if err != nil {
			t.Fatalf("NewConfig(%q) error = %v", file, err)
		}
		if got := c.UnknownKeys(); len(got) != 0 {
			t.Errorf("NewConfig(%q) found unknown keys %+v", file, got)
		}
	}
}

// should write one warning line per key, naming the key, the file and the
// suggestion when there is one.
func TestWarnUnknownKeys_ShouldLogOneLinePerKey(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	warnUnknownKeys(log, "/etc/ssoossh/ssoosshd.yaml", []UnknownKey{
		{Key: "ldap.schmea", Suggestion: "ldap.schema"},
		{Key: "brandnew"},
	})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), buf.String())
	}
	tests := []struct {
		line int
		want []string
	}{
		{0, []string{"level=WARN", "key=ldap.schmea", "file=/etc/ssoossh/ssoosshd.yaml", "did_you_mean=ldap.schema"}},
		{1, []string{"level=WARN", "key=brandnew", "file=/etc/ssoossh/ssoosshd.yaml"}},
	}
	for _, tt := range tests {
		for _, w := range tt.want {
			if !strings.Contains(lines[tt.line], w) {
				t.Errorf("line %d = %q, want it to contain %q", tt.line, lines[tt.line], w)
			}
		}
	}
	if strings.Contains(lines[1], "did_you_mean") {
		t.Errorf("line 1 = %q, want no suggestion", lines[1])
	}
}

// should split mapstructure's names into the path an operator writes.
func TestKeySegments_ShouldSplitMapKeysAndIndexes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"brandnew", "brandnew"},
		{"ldap.fields[groups].atribute", "ldap fields groups atribute"},
		{"a.b[0].c", "a b [0] c"},
		{"a[k][1]", "a k [1]"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			if got := strings.Join(keySegments(tt.in), " "); got != tt.want {
				t.Errorf("keySegments(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// should count a transposition as one edit, and handle empty strings.
func TestEditDistance_ShouldCountEdits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b string
		want int
	}{
		{"schema", "schema", 0},
		{"schmea", "schema", 1},
		{"", "abc", 3},
		{"abc", "", 3},
		{"url", "tls", 3},
		{"idattribute", "id_attribute", 1},
	}
	for _, tt := range tests {
		t.Run(tt.a+"/"+tt.b, func(t *testing.T) {
			t.Parallel()

			if got := editDistance(tt.a, tt.b); got != tt.want {
				t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
