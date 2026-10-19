package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"text/tabwriter"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/server"
)

// newOverrideCommand is `ssoossh-nss override`.
func newOverrideCommand(root *RootCommand) *leaf {
	return &leaf{
		root:  root,
		name:  "override",
		short: "List, set or unset this host's per-user overrides.",
		long: "Edits the host override file (--overrides) atomically and asks the running daemon to " +
			"re-read it. An override replaces the server's uid, gid, home or shell for one user on this " +
			"host only, and never takes a uid or gid outside min_id..max_id.",
		commands: []simplecobra.Commander{
			newOverrideListCommand(root),
			newOverrideSetCommand(root),
			newOverrideUnsetCommand(root),
		},
	}
}

// newOverrideListCommand is `override list`.
func newOverrideListCommand(root *RootCommand) *leaf {
	return &leaf{
		root:  root,
		name:  "list",
		short: "Print the overrides in the override file.",
		args:  cobra.NoArgs,
		run: func(_ context.Context, root *RootCommand, cmd *cobra.Command, _ []string) error {
			overrides, err := config.LoadOverrides(root.overrides)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "USER\tUID\tGID\tHOME\tSHELL")
			for _, name := range slices.Sorted(maps.Keys(overrides)) {
				o := overrides[name]
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", name, idOrDash(o.UID), idOrDash(o.GID), orDash(o.Home), orDash(o.Shell))
			}
			return w.Flush()
		},
	}
}

// overrideFields are `override set`'s flags.
type overrideFields struct {
	uid, gid    uint32
	home, shell string
}

// newOverrideSetCommand is `override set USER`.
func newOverrideSetCommand(root *RootCommand) *leaf {
	var fields overrideFields
	return &leaf{
		root:  root,
		name:  "set",
		use:   "USER [--uid N] [--gid N] [--home PATH] [--shell PATH]",
		short: "Set fields of one user's override; an empty --home or --shell clears that field.",
		args:  cobra.ExactArgs(1),
		flags: func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.Uint32Var(&fields.uid, "uid", 0, "the uid this host gives the user")
			f.Uint32Var(&fields.gid, "gid", 0, "the primary gid this host gives the user")
			f.StringVar(&fields.home, "home", "", "the home directory")
			f.StringVar(&fields.shell, "shell", "", "the login shell")
			cmd.MarkFlagsOneRequired("uid", "gid", "home", "shell")
		},
		run: func(ctx context.Context, root *RootCommand, cmd *cobra.Command, args []string) error {
			return root.setOverride(ctx, cmd, args[0], fields)
		},
	}
}

// setOverride applies the flags `override set` was given to one user's
// override. A uid or gid outside min_id..max_id is refused here, as the
// policy would refuse to serve it.
func (r *RootCommand) setOverride(ctx context.Context, cmd *cobra.Command, user string, fields overrideFields) error {
	cfg, err := config.Load(r.configPath)
	if err != nil {
		return fmt.Errorf("the id range comes from the configuration: %w", err)
	}
	flags := cmd.Flags()
	for name, id := range map[string]uint32{"uid": fields.uid, "gid": fields.gid} {
		if flags.Changed(name) && !cfg.InRange(id) {
			return fmt.Errorf("--%s %d is outside min_id..max_id (%d..%s)", name, id, cfg.MinID, maxOrNone(cfg.MaxID))
		}
	}
	return editOverrides(ctx, r, cmd, func(o config.Overrides) error {
		entry := o[user]
		if flags.Changed("uid") {
			entry.UID = &fields.uid
		}
		if flags.Changed("gid") {
			entry.GID = &fields.gid
		}
		if flags.Changed("home") {
			entry.Home = fields.home
		}
		if flags.Changed("shell") {
			entry.Shell = fields.shell
		}
		if entry.IsZero() {
			delete(o, user)
		} else {
			o[user] = entry
		}
		return nil
	})
}

// newOverrideUnsetCommand is `override unset USER`.
func newOverrideUnsetCommand(root *RootCommand) *leaf {
	return &leaf{
		root:  root,
		name:  "unset",
		use:   "USER",
		short: "Remove one user's override.",
		args:  cobra.ExactArgs(1),
		run: func(ctx context.Context, root *RootCommand, cmd *cobra.Command, args []string) error {
			return editOverrides(ctx, root, cmd, func(o config.Overrides) error {
				if _, ok := o[args[0]]; !ok {
					return fmt.Errorf("%s has no override", args[0])
				}
				delete(o, args[0])
				return nil
			})
		},
	}
}

// editOverrides loads the override file, applies edit, writes it back
// atomically and asks the daemon to reload. A daemon that is not running
// is a warning, not a failure: it reads the file when it starts.
func editOverrides(ctx context.Context, root *RootCommand, cmd *cobra.Command, edit func(config.Overrides) error) error {
	overrides, err := config.LoadOverrides(root.overrides)
	if err != nil {
		return err
	}
	if err := edit(overrides); err != nil {
		return err
	}
	if err := config.WriteOverrides(root.overrides, overrides); err != nil {
		return err
	}
	if _, err := server.CallAdmin(ctx, root.adminSocket, server.AdminRequest{Op: server.AdminReload}); err != nil {
		if errors.Is(err, server.ErrAdminUnavailable) {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning: the daemon is not running; it reads the overrides when it starts")
			return nil
		}
		return err
	}
	return nil
}

// idOrDash formats an optional id.
func idOrDash(id *uint32) string {
	if id == nil {
		return "-"
	}
	return strconv.FormatUint(uint64(*id), 10)
}

// orDash formats an optional string.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// maxOrNone formats max_id, where 0 means no maximum.
func maxOrNone(id uint32) string {
	if id == 0 {
		return "no maximum"
	}
	return strconv.FormatUint(uint64(id), 10)
}
