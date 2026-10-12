package configgolden

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// rotation stands in for an embedded logger whose keys are squashed into
// the block that holds it and carry no tags of their own.
type Rotation struct {
	Filename string
	MaxSize  int
}

// testConfig is the configuration struct the fixtures below load into.
type testConfig struct {
	Server   string   `mapstructure:"server"`
	CAPubkey []string `mapstructure:"capubkey"`
	Pinned   bool     `mapstructure:"-"`
	SSHKey   struct {
		Type string `mapstructure:"type"`
		Size int    `mapstructure:"size"`
	} `mapstructure:"sshkey"`
	Logging struct {
		Rotation `mapstructure:",squash"`

		Level string `mapstructure:"level"`
	} `mapstructure:"logging"`
	HSM *struct {
		Module string `mapstructure:"module"`
		PIN    string `mapstructure:"pin"`
	} `mapstructure:"hsm"`
}

// doc joins fixture lines into a file.
func doc(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// should accept every way a defaults file writes a setting and report each
// way it gets one wrong.
func TestCommentedSettingProblems_ShouldReportOnlyBrokenSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
		want []string // one substring per expected problem, in order
	}{
		{
			name: "should accept a top-level key commented out with no value",
			doc:  doc("# Base URL of the server.", "#server:"),
		},
		{
			name: "should accept a block whose children keep their indentation after the mark",
			doc:  doc("# The key to generate.", "#sshkey:", "#  type: ecdsa", "#  size: 384"),
		},
		{
			name: "should accept a list written under its key",
			doc:  doc("#capubkey:", `#  - "ssh-ed25519 AAAA... old"`, `#  - "ssh-ed25519 AAAA... new"`),
		},
		{
			name: "should accept keys commented out at their own indentation under a commented section",
			doc:  doc("# The token.", "#hsm:", "  # The module.", `  #module: "/usr/lib/softhsm.so"`, "", "  # The PIN.", `  #pin: "1234"`),
		},
		{
			name: "should accept a commented key under a live section",
			doc:  doc("logging:", "  # Rotation.", `  #filename: ""`, "  #maxsize: 0", "", `  level: "info"`),
		},
		{
			name: "should leave prose alone when its first word is not a config key",
			doc:  doc("# wants: the tenant-stable id, not the per-app one.", "#server:"),
		},
		{
			name: "should leave an indented doc-comment example alone",
			doc:  doc("# For example:", "#", "#   server: https://ssh.example.com", "#server:"),
		},
		{
			name: "should report a setting written with a space after the mark",
			doc:  doc("# Base URL of the server.", "# server:", "#capubkey:"),
			want: []string{`line 2: commented-out setting "server" is written "# server:"`},
		},
		{
			name: "should report a nested setting written with a space after the mark",
			doc:  doc("logging:", `  # filename: ""`, `  level: "info"`, "#server:"),
			want: []string{`line 2: commented-out setting "filename"`},
		},
		{
			name: "should report a child indented three spaces under its key",
			doc:  doc("#sshkey:", "#   type: ecdsa"),
			want: []string{"line 1: uncommenting it does not give the setting it shows: line 2 is indented 3 spaces"},
		},
		{
			name: "should report a key indented off the two-space grid",
			doc:  doc("logging:", `   #level: "info"`),
			want: []string{"line 2: uncommenting it does not give the setting it shows: the key is indented 3 spaces at depth 1"},
		},
		{
			name: "should report a value that is not YAML",
			doc:  doc("#capubkey: [unclosed"),
			want: []string{"line 1: uncommenting it does not give the setting it shows: the file no longer parses"},
		},
		{
			name: "should report a key that is already set when uncommented",
			doc:  doc(`server: "https://a"`, `#server: "https://b"`),
			want: []string{"line 2: uncommenting it does not give the setting it shows: the file no longer decodes"},
		},
		{
			name: "should report a block whose lines parse as a scalar",
			doc:  doc("#sshkey:", "#  ecdsa"),
			want: []string{"line 1: uncommenting it does not give the setting it shows: sshkey has 1 lines below it but parsed as a scalar"},
		},
		{
			name: "should report a file with no commented-out settings at all",
			doc:  doc(`server: "https://a"`),
			want: []string{"found no commented-out settings"},
		},
		{
			name: "should report an empty file as having no settings",
			doc:  "",
			want: []string{"found no commented-out settings"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := commentedSettingProblems(tt.doc, testConfig{})
			if len(got) != len(tt.want) {
				t.Fatalf("got %d problems, want %d:\n%s", len(got), len(tt.want), strings.Join(got, "\n"))
			}
			for i, want := range tt.want {
				if !strings.Contains(got[i], want) {
					t.Errorf("problem %d:\n%s\nwant it to contain:\n%s", i, got[i], want)
				}
			}
		})
	}
}

// should name every key a file can use: tagged names, untagged names
// lowercased, a squashed field's children without the field, and nothing
// for a field tagged "-".
func TestConfigKeys_ShouldCollectEveryMapstructureKey(t *testing.T) {
	t.Parallel()

	keys := map[string]bool{}
	configKeys(reflect.TypeOf(testConfig{}), keys, map[reflect.Type]bool{})

	var got []string
	for k := range keys {
		got = append(got, k)
	}
	sort.Strings(got)

	want := []string{"capubkey", "filename", "hsm", "level", "logging", "maxsize", "module", "pin", "server", "size", "sshkey", "type"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// should find a key's node only when every level of its path is a mapping
// holding the next name.
func TestLookup_ShouldFindOnlyAKeyOnItsPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		path  []string
		found bool
	}{
		{name: "should find a nested key", path: []string{"logging", "level"}, found: true},
		{name: "should not find a missing key", path: []string{"logging", "format"}, found: false},
		{name: "should not descend into a scalar", path: []string{"server", "host"}, found: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var root yaml.Node
			if err := yaml.Unmarshal([]byte(doc(`server: "https://a"`, "logging:", `  level: "info"`)), &root); err != nil {
				t.Fatalf("fixture does not parse: %v", err)
			}
			key, _ := lookup(&root, tt.path)
			if (key != nil) != tt.found {
				t.Errorf("found = %v, want %v", key != nil, tt.found)
			}
		})
	}
}
