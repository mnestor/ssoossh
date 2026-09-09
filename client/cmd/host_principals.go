package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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
			"itself: the mapping file adds principals to an account and can never take " +
			"the account's own name away. An unknown account, a missing file, and a file " +
			"that will not load all still print that one line and exit 0 -- a file that " +
			"will not load is reported on stderr rather than by refusing to answer, so " +
			"this host stays consistent with pam_ssoossh, which falls back to the same " +
			"rule when it cannot read the map.",
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

	// A file that is not there yet is not a failure: the account keeps the
	// floor below and nothing more, which is the right answer for a host
	// whose mapping has not been written.
	//
	// A file that is there and will not load -- unreadable, malformed --
	// is a failure, and it is logged, but it still answers with the floor
	// and exits 0. That is deliberately not "fail closed": pam_ssoossh
	// treats a map it cannot load as no map at all and falls back to
	// requiring the certificate to carry the local account name, and the
	// two must not read one file differently. Denying here while sudo
	// still admitted the account would be a worse outcome than either
	// behaviour on its own.
	//
	// The log goes to stderr, where slog's default handler is installed
	// (see installTracing). It must never reach stdout: sshd parses stdout
	// as the principal list, so a diagnostic printed there would be read
	// as a principal.
	mapping, err := principalsmap.LoadFromFile(mappingPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return printPrincipals(username, nil)
		}
		// Error, not Warn: this is a file the operator wrote and the host
		// is now ignoring, and the default client verbosity shows Warn and
		// above, so either would surface. Error says the mapping is not in
		// effect, which is the part worth noticing.
		slog.Error("the principals map could not be loaded; answering with the account name alone",
			"path", mappingPath, "account", username, "error", err)
		return printPrincipals(username, nil)
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
