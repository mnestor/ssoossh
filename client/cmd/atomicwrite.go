package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mnestor/ssoossh/internal/fileperm"
)

// writeFileAtomic writes data to path via a same-directory temp file and
// rename. The temp file must live next to the destination, not in the
// system temp dir: rename cannot cross filesystems, and /tmp is routinely
// a different one from /etc or $HOME. Protection is applied to the temp
// file before the rename so the destination never exists with weaker
// permissions than were asked for.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmpfile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpfile.Name()) // best-effort cleanup; gone already after a successful rename.

	if _, err := tmpfile.Write(data); err != nil {
		tmpfile.Close() // the write error is the one worth reporting.
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmpfile.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	// Against the closed path rather than the open handle: fileperm's
	// Windows half rewrites the file's access list, which is a path
	// operation, and a mode alone would protect nothing there.
	if err := fileperm.Restrict(tmpfile.Name(), perm); err != nil {
		return fmt.Errorf("protect %s: %w", path, err)
	}

	if err := os.Rename(tmpfile.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	slog.Debug("wrote file", "path", path, "bytes", len(data), "mode", fmt.Sprintf("%o", perm))
	return nil
}
