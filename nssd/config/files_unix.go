//go:build unix

package config

import (
	"os"
	"syscall"
)

// fileOwnerUID returns the uid that owns the file info describes.
func fileOwnerUID(info os.FileInfo) (uint32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}

// fileDevIno returns the device and inode of the file info describes, the
// part of a Stamp that tells a replaced file from one edited in place.
func fileDevIno(info os.FileInfo) (dev, ino uint64, ok bool) {
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	// Dev is uint32 on some platforms, hence the conversion.
	return uint64(st.Dev), st.Ino, true //nolint:unconvert // Dev is not uint64 everywhere.
}
