package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via a same-directory temp file and
// rename. The temp file must live next to the destination, not in the
// system temp dir: rename cannot cross filesystems, and /tmp is routinely
// a different one from /etc or $HOME. Permissions are set on the temp file
// before the rename so the destination never exists with the wrong mode.
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
	if err := tmpfile.Chmod(perm); err != nil {
		tmpfile.Close() // the chmod error is the one worth reporting.
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmpfile.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	if err := os.Rename(tmpfile.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	slog.Debug("wrote file", "path", path, "bytes", len(data), "mode", fmt.Sprintf("%o", perm))
	return nil
}
