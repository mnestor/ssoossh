package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// should treat a missing file as an empty mapping — nothing has been added
// yet — but refuse a malformed one: silently starting empty would let the
// next add/remove overwrite whatever the operator actually had.
func TestLoadMapping(t *testing.T) {
	t.Parallel()

	t.Run("should return an empty mapping for a missing file", func(t *testing.T) {
		t.Parallel()
		mapping, err := loadMapping(filepath.Join(t.TempDir(), "absent.json"))
		if err != nil {
			t.Fatalf("loadMapping() error = %v", err)
		}
		if len(mapping) != 0 {
			t.Errorf("loadMapping() = %v, want empty", mapping)
		}
	})

	t.Run("should refuse a malformed file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
			t.Fatalf("seed mapping file: %v", err)
		}
		if _, err := loadMapping(path); err == nil {
			t.Fatal("loadMapping() error = nil, want error for malformed file")
		}
	})

	t.Run("should round-trip through writeMapping", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		want := map[string][]string{"deploy": {"ci", "alice"}, "root": {"admin"}}

		if err := writeMapping(path, want); err != nil {
			t.Fatalf("writeMapping() error = %v", err)
		}
		got, err := loadMapping(path)
		if err != nil {
			t.Fatalf("loadMapping() error = %v", err)
		}
		if len(got) != len(want) || len(got["deploy"]) != 2 || got["root"][0] != "admin" {
			t.Errorf("round-trip mismatch: got %v, want %v", got, want)
		}
	})
}

func TestWriteMapping_ShouldRequireAConfiguredPath(t *testing.T) {
	t.Parallel()

	if err := writeMapping("", map[string][]string{}); err == nil {
		t.Fatal("writeMapping() error = nil, want error for empty path")
	}
}
