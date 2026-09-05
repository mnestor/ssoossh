//go:build windows

package principalsmap

import "fmt"

// inodeOf has no meaning on Windows; the test that calls it skips there.
func inodeOf(string) (uint64, error) {
	return 0, fmt.Errorf("not supported on windows")
}
