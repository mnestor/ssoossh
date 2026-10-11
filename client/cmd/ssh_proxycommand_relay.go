package cmd

import (
	"io"
	"os/exec"
)

// relayChild runs the relay at path as a child process and waits for it,
// for the platforms where handOff cannot replace this process. argv is
// passed whole, program name included, exactly as an exec would see it.
//
// Given this process's own stdio as *os.File, the child inherits the
// handles themselves, so the SSH stream runs between ssh and the relay
// with no copying through here. Returns the child's *exec.ExitError when it
// ran and failed.
//
// Portable rather than Windows-only so the Linux and macOS test runs cover
// it too.
func relayChild(path string, argv, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := &exec.Cmd{
		Path:   path,
		Args:   argv,
		Env:    env,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}
	return cmd.Run()
}
