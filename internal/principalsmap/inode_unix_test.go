//go:build !windows

package principalsmap

import (
	"fmt"
	"os"
	"syscall"
)

// inodeOf returns the file's inode number, which is what distinguishes an
// overwritten file from a replaced one.
func inodeOf(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("stat has no unix inode on this platform")
	}
	return uint64(st.Ino), nil
}
