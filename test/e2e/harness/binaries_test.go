//go:build e2e || resilience || load

package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrebuiltBinaries pins what SSOOSSH_E2E_BIN_DIR accepts. The point of
// the variable is to skip the in-test build, so every way the hand-off can
// be broken has to fail loudly instead of falling back to a build.
func TestPrebuiltBinaries(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]os.FileMode // name -> mode; a mode with os.ModeDir makes a directory
		wantErr string                 // "" means success
	}{
		{
			name:  "should return the directory when both binaries are executable",
			files: map[string]os.FileMode{"ssoosshd": 0o755, "ssoossh": 0o755},
		},
		{
			name:    "should refuse when ssoosshd is missing",
			files:   map[string]os.FileMode{"ssoossh": 0o755},
			wantErr: "ssoosshd",
		},
		{
			name:    "should refuse when ssoossh is missing",
			files:   map[string]os.FileMode{"ssoosshd": 0o755},
			wantErr: "ssoossh",
		},
		{
			name:    "should refuse when a binary lost its executable bit",
			files:   map[string]os.FileMode{"ssoosshd": 0o755, "ssoossh": 0o644},
			wantErr: "is not an executable file",
		},
		{
			name:    "should refuse when a binary is a directory",
			files:   map[string]os.FileMode{"ssoosshd": os.ModeDir | 0o755, "ssoossh": 0o755},
			wantErr: "is not an executable file",
		},
		{
			name:    "should refuse when the directory is empty",
			files:   map[string]os.FileMode{},
			wantErr: "ssoosshd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, mode := range tt.files {
				path := filepath.Join(dir, name)
				if mode&os.ModeDir != 0 {
					if err := os.Mkdir(path, mode.Perm()); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
					t.Fatal(err)
				}
			}

			got, err := prebuiltBinaries(dir)

			if tt.wantErr == "" {
				if err != nil || got != dir {
					t.Fatalf("got (%q, %v), want (%q, nil)", got, err, dir)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got error %v, want one naming %q", err, tt.wantErr)
			}
		})
	}
}

// go test runs a package from its own directory, so a relative
// SSOOSSH_E2E_BIN_DIR has to be read from the module root -- "bin" means
// the bin/ that `go build -o bin/ ./cmd/...` wrote, not test/e2e/bin.
func TestPrebuiltBinaries_ShouldResolveARelativeDirectoryFromTheModuleRoot(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	const rel = "no-such-e2e-bin-dir"

	_, err = prebuiltBinaries(rel)

	if want := filepath.Join(root, rel, "ssoosshd"); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got error %v, want one naming %q", err, want)
	}
}
