package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/bep/simplecobra"

	sshcrypto "github.com/mnestor/ssoossh/internal/crypto/ssh"
	"github.com/mnestor/ssoossh/internal/principalsmap"
)

func newHostMappingCommand(mappingFileFunc func() string) simplecobra.Commander {
	return &simpleCommand{
		name:  "mapping",
		short: "Manage the local principal mapping file.",
		long: "Manages the local principal mapping: a YAML file listing, per local account, " +
			"the certificate principals allowed to assume it. Used by `host principals` to " +
			"answer sshd's AuthorizedPrincipalsCommand, and readable by pam_ssoossh's " +
			"principals-map, so one file can serve both.",
		offline: true,
		commands: []simplecobra.Commander{
			newHostMappingListCommand(mappingFileFunc),
			newHostMappingAddCommand(mappingFileFunc),
			newHostMappingRemoveCommand(mappingFileFunc),
		},
	}
}

func newHostMappingListCommand(mappingFileFunc func() string) simplecobra.Commander {
	return &simpleCommand{
		name:    "list",
		short:   "Print the current principal mapping.",
		offline: true,
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return runHostMappingList(ctx, mappingFileFunc())
		},
	}
}

func runHostMappingList(ctx context.Context, mappingPath string) error {
	mapping, err := loadMapping(mappingPath)
	if err != nil {
		return err
	}

	// Printed as the file itself is spelled, so the output can be
	// redirected back into one. An empty mapping is therefore no output at
	// all rather than a "{}" placeholder: the subset the map is parsed
	// from has no flow-mapping form, so "{}" would not load back.
	out, err := principalsmap.Format(mapping)
	if err != nil {
		return fmt.Errorf("render mapping: %w", err)
	}
	fmt.Print(string(out))
	return nil
}

func newHostMappingAddCommand(mappingFileFunc func() string) simplecobra.Commander {
	cmd := &simpleCommand{
		name:    "add",
		argSpec: "<account> <principal>",
		short:   "Add a principal to an account's mapping.",
		long: "Adds a principal to the given account, deduplicating if already present. " +
			"Validates principal syntax before writing.\n\n" +
			"Takes two arguments, in this order: the local account, then the certificate " +
			"principal allowed to assume it.",
		offline: true,
	}
	cmd.run = func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
		if len(args) < 2 {
			return cmd.usageError(cd)
		}
		account := args[0]
		principal := args[1]

		if err := sshcrypto.ValidatePrincipal(principal); err != nil {
			return fmt.Errorf("invalid principal: %w", err)
		}

		return runHostMappingAdd(account, principal, mappingFileFunc())
	}
	return cmd
}

func runHostMappingAdd(account, principal, mappingPath string) error {
	mapping, err := loadMapping(mappingPath)
	if err != nil {
		return err
	}
	principals := mapping[account]
	if !slices.Contains(principals, principal) {
		principals = append(principals, principal)
		mapping[account] = principals
	}

	return writeMapping(mappingPath, mapping)
}

func newHostMappingRemoveCommand(mappingFileFunc func() string) simplecobra.Commander {
	cmd := &simpleCommand{
		name:    "remove",
		argSpec: "<account> [principal]",
		short:   "Remove a principal or an entire account mapping.",
		long: "Takes the local account, and optionally one of its principals. With both, " +
			"removes that principal from that account (a no-op if it is not present). With " +
			"the account alone, removes the entire account mapping.",
		offline: true,
	}
	cmd.run = func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
		if len(args) < 1 {
			return cmd.usageError(cd)
		}
		account := args[0]

		if len(args) == 1 {
			return runHostMappingRemove(account, "", mappingFileFunc())
		}
		principal := args[1]
		return runHostMappingRemove(account, principal, mappingFileFunc())
	}
	return cmd
}

func runHostMappingRemove(account, principal, mappingPath string) error {
	mapping, err := loadMapping(mappingPath)
	if err != nil {
		return err
	}
	if principal == "" {
		// Remove the entire account mapping.
		delete(mapping, account)
	} else {
		// Remove a specific principal from the account.
		principals := mapping[account]
		idx := slices.Index(principals, principal)
		if idx >= 0 {
			mapping[account] = slices.Delete(principals, idx, idx+1)
			if len(mapping[account]) == 0 {
				delete(mapping, account)
			}
		}
	}

	return writeMapping(mappingPath, mapping)
}

// loadMapping reads the mapping file. A missing file is an empty mapping
// (nothing has been added yet); a malformed one is an error -- silently
// treating it as empty would let the next add/remove overwrite whatever
// the operator actually had.
func loadMapping(path string) (principalsmap.PrincipalsMap, error) {
	if path == "" {
		return principalsmap.PrincipalsMap{}, nil
	}
	mapping, err := principalsmap.LoadFromFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return principalsmap.PrincipalsMap{}, nil
		}
		return nil, err
	}
	return mapping, nil
}

// writeMapping writes the mapping back over the file it came from, keeping
// the ownership and mode an operator set on it. See
// principalsmap.WriteFile for why that matters here and what it costs.
func writeMapping(path string, mapping principalsmap.PrincipalsMap) error {
	if path == "" {
		return fmt.Errorf("no mapping file: --file is empty")
	}
	return principalsmap.WriteFile(path, mapping)
}
