package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/nssd/server"
)

// newCacheCommand is `ssoosshd-nss cache`, sss_cache's counterpart.
func newCacheCommand(root *RootCommand) *leaf {
	var all, allUsers, allGroups bool
	var user, group string
	return &leaf{
		root:  root,
		name:  "cache",
		use:   "-E | -u NAME | -g NAME | -U | -G",
		short: "Mark cached entries stale so their next lookup asks the server.",
		long: "Marks entries stale through the running daemon, like sss_cache: a stale entry is refetched " +
			"on its next lookup and is still served while the server is unreachable. Nothing is deleted. " +
			"Remembered misses for the selection are forgotten too.",
		args: cobra.NoArgs,
		flags: func(cmd *cobra.Command) {
			f := cmd.Flags()
			f.BoolVarP(&all, "everything", "E", false, "every user and group")
			f.StringVarP(&user, "user", "u", "", "one user, by name")
			f.StringVarP(&group, "group", "g", "", "one group, by name")
			f.BoolVarP(&allUsers, "users", "U", false, "every user")
			f.BoolVarP(&allGroups, "groups", "G", false, "every group")
			cmd.MarkFlagsMutuallyExclusive("everything", "user", "group", "users", "groups")
			cmd.MarkFlagsOneRequired("everything", "user", "group", "users", "groups")
		},
		run: func(ctx context.Context, root *RootCommand, cmd *cobra.Command, _ []string) error {
			req := server.AdminRequest{Op: server.AdminInvalidate}
			switch {
			case all:
				req.Users, req.Groups = true, true
			case allUsers:
				req.Users = true
			case allGroups:
				req.Groups = true
			case user != "":
				req.Users, req.Name = true, user
			case group != "":
				req.Groups, req.Name = true, group
			default:
				// not covered: cobra's one-required check runs first.
				return errors.New("choose one of -E, -u, -g, -U or -G")
			}
			resp, err := server.CallAdmin(ctx, root.adminSocket, req)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "marked %d cached entries stale\n", resp.Marked)
			return err
		},
	}
}
