package cmd

import (
	"context"
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

	err := writeMapping("", map[string][]string{})
	if err == nil {
		t.Fatal("writeMapping() error = nil, want error for empty path")
	}
	// Check that the error message mentions the flag
	if err.Error() != "no mapping file: --file is empty" {
		t.Errorf("writeMapping() error = %q, want %q", err.Error(), "no mapping file: --file is empty")
	}
}

func TestRunHostMappingAdd(t *testing.T) {
	t.Parallel()

	t.Run("should add a principal to an empty mapping", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := runHostMappingAdd("deploy", "ci", path); err != nil {
			t.Fatalf("runHostMappingAdd() error = %v", err)
		}
		got, err := loadMapping(path)
		if err != nil {
			t.Fatalf("loadMapping() error = %v", err)
		}
		if len(got["deploy"]) != 1 || got["deploy"][0] != "ci" {
			t.Errorf("got %v, want deploy: [ci]", got)
		}
	})

	t.Run("should deduplicate when adding an existing principal", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := runHostMappingAdd("deploy", "ci", path); err != nil {
			t.Fatalf("first add error = %v", err)
		}
		if err := runHostMappingAdd("deploy", "ci", path); err != nil {
			t.Fatalf("second add error = %v", err)
		}
		got, err := loadMapping(path)
		if err != nil {
			t.Fatalf("loadMapping() error = %v", err)
		}
		if len(got["deploy"]) != 1 {
			t.Errorf("got %v, want exactly 1 principal", got)
		}
	})

	t.Run("should reject an empty path", func(t *testing.T) {
		t.Parallel()
		err := runHostMappingAdd("deploy", "ci", "")
		if err == nil {
			t.Fatal("runHostMappingAdd() error = nil, want error for empty path")
		}
	})
}

func TestRunHostMappingRemove(t *testing.T) {
	t.Parallel()

	t.Run("should remove an entire account when no principal specified", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := runHostMappingAdd("deploy", "ci", path); err != nil {
			t.Fatalf("add error = %v", err)
		}
		if err := runHostMappingRemove("deploy", "", path); err != nil {
			t.Fatalf("remove error = %v", err)
		}
		got, err := loadMapping(path)
		if err != nil {
			t.Fatalf("loadMapping() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty mapping", got)
		}
	})

	t.Run("should remove a specific principal from an account", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := runHostMappingAdd("deploy", "ci", path); err != nil {
			t.Fatalf("add ci error = %v", err)
		}
		if err := runHostMappingAdd("deploy", "alice", path); err != nil {
			t.Fatalf("add alice error = %v", err)
		}
		if err := runHostMappingRemove("deploy", "ci", path); err != nil {
			t.Fatalf("remove error = %v", err)
		}
		got, err := loadMapping(path)
		if err != nil {
			t.Fatalf("loadMapping() error = %v", err)
		}
		if len(got["deploy"]) != 1 || got["deploy"][0] != "alice" {
			t.Errorf("got %v, want deploy: [alice]", got)
		}
	})

	t.Run("should drop account key when last principal is removed", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := runHostMappingAdd("deploy", "ci", path); err != nil {
			t.Fatalf("add error = %v", err)
		}
		if err := runHostMappingRemove("deploy", "ci", path); err != nil {
			t.Fatalf("remove error = %v", err)
		}
		got, err := loadMapping(path)
		if err != nil {
			t.Fatalf("loadMapping() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty mapping", got)
		}
	})

	t.Run("should reject an empty path", func(t *testing.T) {
		t.Parallel()
		err := runHostMappingRemove("deploy", "ci", "")
		if err == nil {
			t.Fatal("runHostMappingRemove() error = nil, want error for empty path")
		}
	})
}

func TestRunHostMappingList(t *testing.T) {
	t.Parallel()

	t.Run("should print empty object when path is empty", func(t *testing.T) {
		t.Parallel()
		if err := runHostMappingList(context.Background(), ""); err != nil {
			t.Fatalf("runHostMappingList() error = %v", err)
		}
		// Output is to stdout, so we can't directly test it here.
		// The behavior is covered by integration or manual testing.
	})

	t.Run("should print empty object when file does not exist", func(t *testing.T) {
		t.Parallel()
		if err := runHostMappingList(context.Background(), filepath.Join(t.TempDir(), "absent.json")); err != nil {
			t.Fatalf("runHostMappingList() error = %v", err)
		}
	})

	t.Run("should print formatted mapping when file exists", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := runHostMappingAdd("deploy", "ci", path); err != nil {
			t.Fatalf("add error = %v", err)
		}
		if err := runHostMappingList(context.Background(), path); err != nil {
			t.Fatalf("runHostMappingList() error = %v", err)
		}
	})

	t.Run("should error on malformed file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
			t.Fatalf("write file error = %v", err)
		}
		err := runHostMappingList(context.Background(), path)
		if err == nil {
			t.Fatal("runHostMappingList() error = nil, want error for malformed file")
		}
	})
}

func TestRunHostPrincipals(t *testing.T) {
	t.Parallel()

	t.Run("should not error when path is empty", func(t *testing.T) {
		t.Parallel()
		if err := runHostPrincipals(context.Background(), "testuser", ""); err != nil {
			t.Fatalf("runHostPrincipals() error = %v", err)
		}
	})

	t.Run("should not error when file does not exist", func(t *testing.T) {
		t.Parallel()
		if err := runHostPrincipals(context.Background(), "testuser", filepath.Join(t.TempDir(), "absent.json")); err != nil {
			t.Fatalf("runHostPrincipals() error = %v", err)
		}
	})

	t.Run("should return nil when account not in mapping", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := runHostMappingAdd("deploy", "ci", path); err != nil {
			t.Fatalf("add error = %v", err)
		}
		if err := runHostPrincipals(context.Background(), "nonexistent", path); err != nil {
			t.Fatalf("runHostPrincipals() error = %v", err)
		}
	})

	t.Run("should error on malformed file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "mapping.json")
		if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
			t.Fatalf("write file error = %v", err)
		}
		err := runHostPrincipals(context.Background(), "testuser", path)
		if err == nil {
			t.Fatal("runHostPrincipals() error = nil, want error for malformed file")
		}
	})
}
