package principalsmap

import (
	"os"
	"path/filepath"
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
