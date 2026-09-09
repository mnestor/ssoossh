package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/principalsmap"
)

func newHostPrincipalsCommand(mappingFileFunc func() string) simplecobra.Commander {
	cmd := &simpleCommand{
		name:    "principals",
		argSpec: "<username>",
		short:   "Print the local principal mapping for sshd's AuthorizedPrincipalsCommand.",
		// The "must never touch the network" below is enforced, not just
		// documented: offline makes root's PreRun skip the CA fetch, so
		// there is no server round-trip anywhere in this command's path.
		offline: true,
		long: "Implements AuthorizedPrincipalsCommand. Called on every login attempt, and " +
			"must never touch the network -- it answers only from whatever local mapping " +
			"files were written. It needs no privilege beyond read access to the mapping " +
			"file, so give sshd's AuthorizedPrincipalsCommandUser a dedicated unprivileged " +
			"account rather than root. Expects one argument: the local username to look " +
			"up. Prints one principal per line, always including the looked-up name " +
			"itself, so an unknown account or a missing file still prints that one line. " +
			"An unreadable or malformed file exits non-zero and prints nothing.",
	}
	cmd.run = func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
		if len(args) < 1 {
			return cmd.usageError(cd)
		}
		username := args[0]
		return runHostPrincipals(ctx, username, mappingFileFunc())
	}
	return cmd
}

func runHostPrincipals(ctx context.Context, username, mappingPath string) error {
	if mappingPath == "" {
		return printPrincipals(username, nil)
	}

	// A file that is not there yet is not an error: the account keeps the
	// floor below and nothing more, which is the right answer for a host
	// whose mapping has not been written. Anything else -- unreadable,
	// malformed -- is reported, because answering for a file that exists
	// and says otherwise would apply a policy the operator did not write.
	//
	// Note the error path prints nothing at all, floor included. sshd
	// refuses the login on a non-zero exit either way, and a command that
	// printed a usable principal while failing would be inviting whoever
	// reads its output to use half an answer.
	mapping, err := principalsmap.LoadFromFile(mappingPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return printPrincipals(username, nil)
		}
		return err
	}

	return printPrincipals(username, mapping[username])
}

// printPrincipals writes the account's principals one per line, with the
// account name itself guaranteed to be among them.
//
// The floor is what sshd does on its own when no AuthorizedPrincipalsCommand
// is configured -- a certificate carrying the target account name may assume
// it -- and what pam_ssoossh falls back to when it cannot read the map at
// all. Guaranteeing it here means installing this command no longer takes
// that away, which is what made an identity's own principal unusable on a
// host whose mapping did not restate it.
//
// Appended rather than prepended, and only when absent: a mapping that
// already lists the account keeps the order the file gave, so anything
// parsing this output sees the same lines in the same places as before.
func printPrincipals(username string, mapped []string) error {
	// No account name means nothing to floor with -- sshd always passes
	// %u, so this is a hand-run command with an empty argument, and a
	// blank line is not a principal.
	if username == "" {
		return printLines(mapped)
	}

	if slices.Contains(mapped, username) {
		return printLines(mapped)
	}
	return printLines(append(slices.Clone(mapped), username))
}

// printLines writes one principal per line. Split out so the floor logic
// above reads as the decision it is, with the printing in one place.
func printLines(principals []string) error {
	for _, p := range principals {
		fmt.Println(p)
	}
	return nil
}
