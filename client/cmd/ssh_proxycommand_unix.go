//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// handOff replaces this process with the relay, so ssh's pipes run straight
// to it and nothing of ours stays in the path. name is found on PATH the way
// ssh's own ProxyCommand would find it: execve does no lookup, so a bare
// "nc" used to fail where "/usr/bin/nc" worked.
func handOff(name string, argv, env []string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	// not covered: a successful exec replaces the test binary itself.
	return syscall.Exec(path, argv, env)
}
