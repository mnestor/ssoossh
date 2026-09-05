package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/principalsmap"
)

func newHostPrincipalsCommand(mappingFileFunc func() string) simplecobra.Commander {
	return &simpleCommand{
		name:  "principals",
		short: "Print the local principal mapping for sshd's AuthorizedPrincipalsCommand.",
		// The "must never touch the network" below is enforced, not just
		// documented: offline makes root's PreRun skip the CA fetch, so
		// there is no server round-trip anywhere in this command's path.
		offline: true,
		long: "Implements AuthorizedPrincipalsCommand. Called on every login attempt, and " +
			"must never touch the network -- it answers only from whatever local mapping " +
			"files were written. It needs no privilege beyond read access to the mapping " +
			"file, so give sshd's AuthorizedPrincipalsCommandUser a dedicated unprivileged " +
			"account rather than root. Expects one argument: the local username to look " +
			"up. Prints one principal per line; unknown account or missing file exits 0 " +
			"with no output (sshd treats as no principals). An unreadable or malformed " +
			"file exits non-zero.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("usage: ssoossh host principals <username>")
			}
			username := args[0]
			return runHostPrincipals(ctx, username, mappingFileFunc())
		},
	}
}

func runHostPrincipals(ctx context.Context, username, mappingPath string) error {
	if mappingPath == "" {
		return nil
	}

	// A file that is not there yet is not an error: sshd reads no output as
	// no principals, which is the right answer for a host whose mapping has
	// not been written. Anything else -- unreadable, malformed -- is
	// reported, because answering "no principals" for a file that exists
	// and says otherwise would quietly deny every login it authorizes.
	mapping, err := principalsmap.LoadFromFile(mappingPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	for _, p := range mapping[username] {
		fmt.Println(p)
	}
	return nil
}
