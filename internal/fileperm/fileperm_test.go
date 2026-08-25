package fileperm

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// modeOnThisOS is the mode a file written with perm reports back here.
// Windows has no POSIX permission bits, so Go answers 0666 for anything
// writable whatever it was created with; the access list Restrict writes on
// that platform is what fileperm_windows_test.go checks.
func modeOnThisOS(perm os.FileMode) os.FileMode {
	if runtime.GOOS == "windows" {
		return 0o666
	}
	return perm
}

// should apply the requested mode, and should keep doing so for a file that
// already exists with a wider one. The second half is the case os.WriteFile
// gets wrong: its mode argument only applies when it creates the file, so a
// key rewritten over a world-readable stub would stay world-readable.
func TestRestrict_ShouldApplyTheRequestedMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		created os.FileMode
		want    os.FileMode
	}{
		{name: "should narrow a new file to owner only", created: 0o600, want: 0o600},
		{name: "should narrow a file that already exists too wide", created: 0o644, want: 0o600},
		{name: "should leave a mode meant to be readable alone", created: 0o644, want: 0o644},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, []byte("secret"), tt.created); err != nil {
				t.Fatalf("write file: %v", err)
			}

			if err := Restrict(path, tt.want); err != nil {
				t.Fatalf("Restrict() error = %v", err)
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if got := info.Mode().Perm(); got != modeOnThisOS(tt.want) {
				t.Errorf("mode = %o, want %o", got, modeOnThisOS(tt.want))
			}
		})
	}
}

// The caller writes a key and then restricts it. If the restrict half fails
// silently the key is on disk unprotected, so the error has to come back and
// name the file.
func TestRestrict_ShouldFailWhenTheFileIsNotThere(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "absent")

	err := Restrict(path, 0o600)
	if err == nil {
		t.Fatal("expected an error for a file that does not exist")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("got %v, want it to unwrap to os.ErrNotExist", err)
	}
}
