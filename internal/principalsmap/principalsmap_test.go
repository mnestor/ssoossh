package principalsmap

// Test methodology: table-driven tests over the accepted file format
// (documented on the package), plus the same behavioural tests Allowed has
// always had. The parse cases are written against what gopkg.in/yaml.v3
// used to do with each input, since replacing it must not change which
// files load.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeMapFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	return path
}

func TestParse_ShouldAcceptTheDocumentedFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want PrincipalsMap
	}{
		{
			name: "should parse an account with a block list",
			in:   "alice:\n  - alice\n  - admin\n",
			want: PrincipalsMap{"alice": {"alice", "admin"}},
		},
		{
			name: "should parse several accounts",
			in:   "alice:\n  - admin\nbob:\n  - bob\n",
			want: PrincipalsMap{"alice": {"admin"}, "bob": {"bob"}},
		},
		{
			name: "should accept list items at any indentation, including none",
			in:   "alice:\n- admin\n    - ops\n",
			want: PrincipalsMap{"alice": {"admin", "ops"}},
		},
		{
			name: "should treat an account with nothing under it as allowing nobody",
			in:   "alice:\n",
			want: PrincipalsMap{"alice": nil},
		},
		{
			name: "should read null as an empty list",
			in:   "alice: null\n",
			want: PrincipalsMap{"alice": nil},
		},
		{
			name: "should read a tilde as an empty list",
			in:   "alice: ~\n",
			want: PrincipalsMap{"alice": nil},
		},
		{
			name: "should read an empty flow sequence as an empty list",
			in:   "alice: []\n",
			want: PrincipalsMap{"alice": {}},
		},
		{
			name: "should read a flow sequence of principals",
			in:   "alice: [admin, ops]\n",
			want: PrincipalsMap{"alice": {"admin", "ops"}},
		},
		{
			name: "should ignore blank lines and whole-line comments",
			in:   "# who may become alice\n\nalice:\n\n  - admin\n",
			want: PrincipalsMap{"alice": {"admin"}},
		},
		{
			name: "should ignore a trailing comment on either kind of line",
			in:   "alice:   # the admin account\n  - admin # on call\n",
			want: PrincipalsMap{"alice": {"admin"}},
		},
		{
			name: "should keep a hash that is part of a value rather than a comment",
			in:   "alice:\n  - admin#1\n",
			want: PrincipalsMap{"alice": {"admin#1"}},
		},
		{
			name: "should strip double quotes",
			in:   "\"alice\":\n  - \"admin\"\n",
			want: PrincipalsMap{"alice": {"admin"}},
		},
		{
			name: "should strip single quotes",
			in:   "'alice':\n  - 'admin'\n",
			want: PrincipalsMap{"alice": {"admin"}},
		},
		{
			name: "should keep a comment marker inside a quoted value",
			in:   "alice:\n  - 'admin # not a comment'\n",
			want: PrincipalsMap{"alice": {"admin # not a comment"}},
		},
		{
			name: "should accept CRLF line endings",
			in:   "alice:\r\n  - admin\r\n",
			want: PrincipalsMap{"alice": {"admin"}},
		},
		{
			name: "should parse an empty file as an empty map",
			in:   "",
			want: PrincipalsMap{},
		},
		{
			name: "should parse a file of only comments as an empty map",
			in:   "# nothing here yet\n",
			want: PrincipalsMap{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parse([]byte(tt.in))
			if err != nil {
				t.Fatalf("parse() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parse() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParse_ShouldRejectWhatItCannotReadFaithfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{name: "should reject an unterminated flow sequence", in: "alice: [this is not: valid\n"},
		{name: "should reject a scalar where a list belongs", in: "alice: admin\n"},
		{name: "should reject an empty string where a list belongs", in: "alice: \"\"\n"},
		{name: "should reject a principal with no account above it", in: "  - admin\n"},
		{name: "should reject a principal after an inline list closed the account", in: "alice: [admin]\n  - ops\n"},
		{name: "should reject a duplicate account", in: "alice:\n  - admin\nalice:\n  - ops\n"},
		{name: "should reject tab indentation", in: "alice:\n\t- admin\n"},
		{name: "should reject a line that is neither an account nor an item", in: "alice\n"},
		{name: "should reject an empty account name", in: ":\n  - admin\n"},
		{name: "should reject a bare dash with no principal", in: "alice:\n  -\n"},
		{name: "should reject an item with no space after the dash", in: "alice:\n  -admin\n"},
		{name: "should reject an empty entry in a flow sequence", in: "alice: [admin, , ops]\n"},
		{name: "should reject an unterminated quoted value", in: "alice:\n  - 'admin\n"},
		{name: "should reject an unterminated quoted value in a flow sequence", in: "alice: ['admin]\n"},
		{name: "should reject escapes in a quoted account name", in: "'a\\b':\n  - admin\n"},
		{name: "should reject escapes in a quoted value", in: "alice:\n  - \"ad\\u006din\"\n"},
		{name: "should reject a nested mapping", in: "alice:\n  admin:\n    - ops\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, err := parse([]byte(tt.in)); err == nil {
				t.Errorf("parse() = %#v, want an error", got)
			}
		})
	}
}

// The parse error reaches an operator through pam_ssoossh's syslog warning,
// where "line 3" is the difference between a fixable report and a shrug.
func TestParse_ShouldNameTheOffendingLine(t *testing.T) {
	t.Parallel()

	_, err := parse([]byte("alice:\n  - admin\nbob: nope\n"))
	if err == nil {
		t.Fatal("expected an error for a scalar where a list belongs")
	}
	if want := "line 3"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the offending line (%q)", err, want)
	}
}

func TestLoadFromFile_ShouldParseAValidMap(t *testing.T) {
	path := writeMapFile(t, "alice:\n  - alice\n  - admin\n")

	m, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.Allowed("alice", []string{"admin"}) {
		t.Error("expected admin to be allowed for alice per the loaded map")
	}
}

func TestLoadFromFile_ShouldErrorOnMalformedYAML(t *testing.T) {
	path := writeMapFile(t, "alice: [this is not: valid\n")

	if _, err := LoadFromFile(path); err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

func TestLoadFromFile_ShouldErrorWhenFileIsMissing(t *testing.T) {
	if _, err := LoadFromFile(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestAllowed_ShouldAcceptAPrincipalListedForTheAccount(t *testing.T) {
	m := PrincipalsMap{"alice": {"alice", "admin"}}

	if !m.Allowed("alice", []string{"admin"}) {
		t.Error("expected admin to be allowed for alice")
	}
}

func TestAllowed_ShouldRejectAPrincipalNotListedForTheAccount(t *testing.T) {
	m := PrincipalsMap{"alice": {"alice"}}

	if m.Allowed("alice", []string{"bob"}) {
		t.Error("expected bob to be rejected for alice")
	}
}

func TestAllowed_ShouldRejectAnAccountAbsentFromTheMap(t *testing.T) {
	m := PrincipalsMap{"alice": {"alice"}}

	if m.Allowed("carol", []string{"alice"}) {
		t.Error("expected carol, who has no entry in the map, to be rejected")
	}
}

func TestAllowed_ShouldAcceptWhenAnyOfSeveralCertPrincipalsMatches(t *testing.T) {
	m := PrincipalsMap{"alice": {"admin"}}

	if !m.Allowed("alice", []string{"bob", "admin", "carol"}) {
		t.Error("expected a match found anywhere in the certificate's principals to be accepted")
	}
}

// An account declared with no principals under it allows nobody — the
// entry exists, so the exact-match fallback in pam_ssoossh does not apply
// and nothing may assume the account.
func TestAllowed_ShouldRejectEveryPrincipalForAnAccountWithAnEmptyList(t *testing.T) {
	m, err := parse([]byte("alice:\n"))
	if err != nil {
		t.Fatalf("parse() error = %v", err)
	}

	if m.Allowed("alice", []string{"alice", "admin"}) {
		t.Error("expected an account with no principals listed to allow nobody")
	}
}
