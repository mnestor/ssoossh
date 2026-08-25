package cmd

import (
	"os"
	"runtime"
)

// wantPerm translates a POSIX mode into the mode a file written with it
// actually reports back on the platform running the test.
//
// Windows has no POSIX permission bits. os.Chmod there only toggles the
// read-only attribute, so Go reports every writable file as 0666 whatever
// mode it was created with, and asserting 0600 on a private key fails on
// client-matrix's Windows leg for a reason that says nothing about the code
// under test. The assertion still catches a file that came out read-only,
// which is the one mode distinction Windows does keep.
func wantPerm(perm os.FileMode) os.FileMode {
	if runtime.GOOS == "windows" {
		return 0o666
	}
	return perm
}
