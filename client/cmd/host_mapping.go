package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/bep/simplecobra"

	sshcrypto "github.com/mnestor/ssoossh/internal/crypto/ssh"
)

func newHostMappingCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "mapping",
		short: "Manage the local principal mapping file.",
		long: "Manages the local principal mapping (JSON object: account → []string of principals). " +
			"Used by `host principals` to answer sshd's AuthorizedPrincipalsCommand.",
		offline: true,
		commands: []simplecobra.Commander{
			newHostMappingListCommand(),
			newHostMappingAddCommand(),
			newHostMappingRemoveCommand(),
		},
	}
}

func newHostMappingListCommand() simplecobra.Commander {
	return &simpleCommand{
		name:    "list",
		short:   "Print the current principal mapping.",
		offline: true,
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			cfg := root.Config()
			mappingPath := cfg.PrincipalMappingFile
			if mappingPath == "" {
				fmt.Println("{}")
				return nil
			}

			data, err := os.ReadFile(mappingPath)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("{}")
					return nil
				}
				return fmt.Errorf("read mapping file: %w", err)
			}

			var mapping map[string][]string
			if err := json.Unmarshal(data, &mapping); err != nil {
				return fmt.Errorf("malformed mapping file: %w", err)
			}

			out, err := json.MarshalIndent(mapping, "", "  ")
			if err != nil {
				return fmt.Errorf("encode mapping: %w", err)
			}
			fmt.Println(string(out))
			return nil
		},
	}
}

func newHostMappingAddCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "add",
		short: "Add a principal to an account's mapping.",
		long: "Adds a principal to the given account, deduplicating if already present. " +
			"Validates principal syntax before writing.",
		offline: true,
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("usage: ssoossh host mapping add <account> <principal>")
			}
			account := args[0]
			principal := args[1]

			if err := sshcrypto.ValidatePrincipal(principal); err != nil {
				return fmt.Errorf("invalid principal: %w", err)
			}

			mapping, err := loadMapping(root.Config().PrincipalMappingFile)
			if err != nil {
				return err
			}
			principals := mapping[account]
			if !slices.Contains(principals, principal) {
				principals = append(principals, principal)
				mapping[account] = principals
			}

			return writeMapping(root.Config().PrincipalMappingFile, mapping)
		},
	}
}

func newHostMappingRemoveCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "remove",
		short: "Remove a principal or an entire account mapping.",
		long: "With two arguments, removes the principal from the account (no-op if not present). " +
			"With one argument, removes the entire account mapping.",
		offline: true,
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("usage: ssoossh host mapping remove <account> [principal]")
			}
			account := args[0]

			mapping, err := loadMapping(root.Config().PrincipalMappingFile)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				delete(mapping, account)
			} else {
				principal := args[1]
				principals := mapping[account]
				idx := slices.Index(principals, principal)
				if idx >= 0 {
					mapping[account] = slices.Delete(principals, idx, idx+1)
					if len(mapping[account]) == 0 {
						delete(mapping, account)
					}
				}
			}

			return writeMapping(root.Config().PrincipalMappingFile, mapping)
		},
	}
}

// loadMapping reads the mapping file. A missing file is an empty mapping
// (nothing has been added yet); a malformed one is an error — silently
// treating it as empty would let the next add/remove overwrite whatever
// the operator actually had.
func loadMapping(path string) (map[string][]string, error) {
	if path == "" {
		return make(map[string][]string), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string][]string), nil
		}
		return nil, fmt.Errorf("read mapping file: %w", err)
	}
	var mapping map[string][]string
	if err := json.Unmarshal(data, &mapping); err != nil {
		return nil, fmt.Errorf("malformed mapping file %s: %w", path, err)
	}
	if mapping == nil {
		mapping = make(map[string][]string)
	}
	return mapping, nil
}

func writeMapping(path string, mapping map[string][]string) error {
	if path == "" {
		return fmt.Errorf("principal_mapping_file not configured")
	}

	data, err := json.MarshalIndent(mapping, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mapping: %w", err)
	}

	return writeFileAtomic(path, data, 0644)
}
