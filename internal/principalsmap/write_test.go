package principalsmap

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestFormat should render each accepted shape in the subset parse reads.
func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		in   PrincipalsMap
		want string
	}{
		{
			name: "empty map renders nothing",
			in:   PrincipalsMap{},
			want: "",
		},
		{
			name: "one account with one principal",
			in:   PrincipalsMap{"alice": {"alice"}},
			want: "alice:\n  - alice\n",
		},
		{
			name: "principals keep the order they were added",
			in:   PrincipalsMap{"deploy": {"bob", "alice"}},
			want: "deploy:\n  - bob\n  - alice\n",
		},
		{
			name: "accounts are sorted by name",
			in:   PrincipalsMap{"carol": {"carol"}, "alice": {"alice"}, "bob": {"bob"}},
			want: "alice:\n  - alice\nbob:\n  - bob\ncarol:\n  - carol\n",
		},
		{
			name: "an account with no principals is a bare key",
			in:   PrincipalsMap{"root": nil},
			want: "root:\n",
		},
		{
			name: "an account with an empty list is a bare key",
			in:   PrincipalsMap{"root": {}},
			want: "root:\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Format(tc.in)
			if err != nil {
				t.Fatalf("Format() error = %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("Format() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFormatQuotesWhatWouldNotSurvive should quote any value that would read
// back as something other than itself.
func TestFormatQuotesWhatWouldNotSurvive(t *testing.T) {
	tests := []struct {
		name string
		in   PrincipalsMap
		want string
	}{
		{"colon in an account", PrincipalsMap{"a:b": {"x"}}, "\"a:b\":\n  - x\n"},
		{"hash in an account", PrincipalsMap{"a#b": {"x"}}, "\"a#b\":\n  - x\n"},
		{"leading dash in an account", PrincipalsMap{"-a": {"x"}}, "\"-a\":\n  - x\n"},
		{"empty account name", PrincipalsMap{"": {"x"}}, "\"\":\n  - x\n"},
		{"account spelled null", PrincipalsMap{"null": {"x"}}, "\"null\":\n  - x\n"},
		{"account spelled tilde", PrincipalsMap{"~": {"x"}}, "\"~\":\n  - x\n"},
		{"surrounding space", PrincipalsMap{" a ": {"x"}}, "\" a \":\n  - x\n"},
		{"colon in a principal", PrincipalsMap{"a": {"x:y"}}, "a:\n  - \"x:y\"\n"},
		{"double quote in a value uses single quotes", PrincipalsMap{"a": {`x"y`}}, "a:\n  - 'x\"y'\n"},
		{"single quote in a value uses double quotes", PrincipalsMap{"a": {"x'y"}}, "a:\n  - \"x'y\"\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Format(tc.in)
			if err != nil {
				t.Fatalf("Format() error = %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("Format() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFormatRefusesWhatCannotRoundTrip should error rather than write a value
// the parser would reject or read back differently.
func TestFormatRefusesWhatCannotRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   PrincipalsMap
		want string
	}{
		{"newline in an account", PrincipalsMap{"a\nb": {"x"}}, "line break"},
		{"newline in a principal", PrincipalsMap{"a": {"x\ny"}}, "line break"},
		{"carriage return", PrincipalsMap{"a": {"x\ry"}}, "line break"},
		{"backslash", PrincipalsMap{"a": {`x\y`}}, "backslash"},
		{"both kinds of quote", PrincipalsMap{"a": {`x"y'z`}}, "both kinds of quote"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Format(tc.in)
			if err == nil {
				t.Fatal("Format() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Format() error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestFormatRoundTrips should parse back to the same mapping for every shape
// Format emits, which is the guarantee the writer exists to keep.
func TestFormatRoundTrips(t *testing.T) {
	tests := []struct {
		name string
		in   PrincipalsMap
	}{
		{"ordinary", PrincipalsMap{"alice": {"alice", "admin"}, "deploy": {"alice", "bob"}}},
		{"account with no principals", PrincipalsMap{"root": nil}},
		{"quoted account", PrincipalsMap{"a:b": {"x"}}},
		{"account carrying a quote", PrincipalsMap{`a"b`: {"x"}}},
		{"quoted principal", PrincipalsMap{"a": {"x#y"}}},
		{"value carrying a quote", PrincipalsMap{"a": {`x"y`}}},
		{"account named null", PrincipalsMap{"null": {"x"}}},
		{"empty map", PrincipalsMap{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := Format(tc.in)
			if err != nil {
				t.Fatalf("Format() error = %v", err)
			}
			got, err := parse(data)
			if err != nil {
				t.Fatalf("parse(%q) error = %v", data, err)
			}
			if !equalMaps(got, tc.in) {
				t.Errorf("round trip = %#v, want %#v (via %q)", got, tc.in, data)
			}
		})
	}
}

// TestWriteFileCreatesAReadableFile should create the file readable by other
// accounts, because sshd reads it as a different user than the root writing
// it.
func TestWriteFileCreatesAReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has no POSIX mode bits to assert on")
	}
	path := filepath.Join(t.TempDir(), "principals.yaml")

	if err := WriteFile(path, PrincipalsMap{"alice": {"alice"}}); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("new file mode = %o, want 644", got)
	}
}

// TestWriteFilePreservesTheExistingMode should leave an operator's mode alone
// on rewrite. This is the whole reason the write is in place rather than a
// temp file and a rename, which would replace the file and reset it.
func TestWriteFilePreservesTheExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has no POSIX mode bits to assert on")
	}
	path := filepath.Join(t.TempDir(), "principals.yaml")
	//nolint:gosec // G306: 0640 is the mode under test -- the point is that WriteFile leaves it alone.
	if err := os.WriteFile(path, []byte("alice:\n  - alice\n"), 0o640); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteFile(path, PrincipalsMap{"alice": {"alice", "admin"}}); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("mode after rewrite = %o, want 640 preserved", got)
	}
}

// TestWriteFileKeepsTheSameInode should overwrite the file rather than replace
// it, so anything holding the path by identity -- ownership, an ACL, a hard
// link -- still refers to the file that was written.
func TestWriteFileKeepsTheSameInode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inode identity is not the model on windows")
	}
	path := filepath.Join(t.TempDir(), "principals.yaml")
	//nolint:gosec // G306: 0644 is what a real mapping file carries; this seeds one to overwrite.
	if err := os.WriteFile(path, []byte("alice:\n  - alice\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	before, err := inodeOf(path)
	if err != nil {
		t.Fatalf("inode before: %v", err)
	}

	if err := WriteFile(path, PrincipalsMap{"bob": {"bob"}}); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	after, err := inodeOf(path)
	if err != nil {
		t.Fatalf("inode after: %v", err)
	}
	if before != after {
		t.Errorf("inode changed from %d to %d; the file was replaced, not overwritten", before, after)
	}
}

// TestWriteFileTruncatesWhatItShortens should leave no tail of the previous,
// longer content behind -- which would either invent a principal or make the
// file unparseable.
func TestWriteFileTruncatesWhatItShortens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "principals.yaml")
	if err := WriteFile(path, PrincipalsMap{"alice": {"alice", "admin"}, "deploy": {"bob"}}); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	if err := WriteFile(path, PrincipalsMap{"alice": {"alice"}}); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "alice:\n  - alice\n" {
		t.Errorf("file = %q, want only the shorter mapping", data)
	}
}

// TestWriteFileRoundTripsThroughLoadFromFile should read back exactly what was
// written, through the same entry point pam_ssoossh and host principals use.
func TestWriteFileRoundTripsThroughLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "principals.yaml")
	want := PrincipalsMap{"alice": {"alice", "admin"}, "deploy": {"alice", "bob"}, "root": nil}

	if err := WriteFile(path, want); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	if !equalMaps(got, want) {
		t.Errorf("LoadFromFile() = %#v, want %#v", got, want)
	}
}

// TestWriteFileRefusesAnUnwritablePath should report the open failure rather
// than reporting success for a write that never happened.
func TestWriteFileRefusesAnUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "principals.yaml")

	err := WriteFile(path, PrincipalsMap{"alice": {"alice"}})
	if err == nil {
		t.Fatal("WriteFile() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "open principals map for writing") {
		t.Errorf("WriteFile() error = %v, want it to name the open failure", err)
	}
}

// TestWriteFileRefusesWhatFormatRefuses should not create or touch the file
// when the mapping cannot be expressed.
func TestWriteFileRefusesWhatFormatRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "principals.yaml")

	err := WriteFile(path, PrincipalsMap{"a": {"x\ny"}})
	if err == nil {
		t.Fatal("WriteFile() error = nil, want an error")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("WriteFile() created the file for a mapping it refused")
	}
}

// equalMaps compares two mappings, treating a nil principal list and an empty
// one as the same thing: both mean an account nobody may assume, and Format
// writes them identically.
func equalMaps(a, b PrincipalsMap) bool {
	if len(a) != len(b) {
		return false
	}
	for account, want := range b {
		got, ok := a[account]
		if !ok {
			return false
		}
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}
