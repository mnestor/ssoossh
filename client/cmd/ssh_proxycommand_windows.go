//go:build windows

package cmd

import (
	"os"
	"os/exec"
)

// handOff runs the relay as a child wired to this process's own stdio,
// since Windows has no exec: syscall.Exec returns EWINDOWS there, which made
// proxycommand fail every time. name is found on PATH the way ssh's own
// ProxyCommand would find it.
func handOff(name string, argv, env []string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	return relayChild(path, argv, env, os.Stdin, os.Stdout, os.Stderr)
}
