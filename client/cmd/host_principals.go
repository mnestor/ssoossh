package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/bep/simplecobra"
)

func newHostPrincipalsCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "principals",
		short: "Print the local principal mapping for sshd's AuthorizedPrincipalsCommand.",
		// The "must never touch the network" below is enforced, not just
		// documented: offline makes root's PreRun skip the CA fetch, so
		// there is no server round-trip anywhere in this command's path.
		offline: true,
		long: "Implements AuthorizedPrincipalsCommand. Runs as root, called on every login " +
			"attempt, and must never touch the network — it answers only from whatever " +
			"local mapping files were written. Expects one argument: the local username to " +
			"look up. Prints one principal per line; unknown account or missing file exits " +
			"0 with no output (sshd treats as no principals). Malformed file exits non-zero.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("usage: ssoossh host principals <username>")
			}
			username := args[0]

			cfg := root.Config()
			mappingPath := cfg.PrincipalMappingFile
			if mappingPath == "" {
				return nil
			}

			data, err := os.ReadFile(mappingPath)
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return fmt.Errorf("read principal mapping file: %w", err)
			}

			var mapping map[string][]string
			if err := json.Unmarshal(data, &mapping); err != nil {
				return fmt.Errorf("malformed principal mapping file: %w", err)
			}

			principals, ok := mapping[username]
			if !ok {
				return nil
			}

			for _, p := range principals {
				fmt.Println(p)
			}
			return nil
		},
	}
}
