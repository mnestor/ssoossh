package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/nssd/server"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

// errStatusFailed is status's exit when a check failed or the daemon did
// not answer, after everything it could find out was printed.
var errStatusFailed = errors.New("status: a check failed")

// newStatusCommand is `ssoosshd-nss status`.
func newStatusCommand(root *RootCommand) *leaf {
	return &leaf{
		root:  root,
		name:  "status",
		short: "Show the daemon's state and check that this host consults it.",
		long: "Prints the directory connection's state (online, offline, unauthorized or rate limited), the " +
			"server, the fleet key id, the cache counts and the last error, from the running daemon; then " +
			"whether nsswitch.conf lists systemd on its passwd, group and shadow lines, and whether nscd " +
			"or unscd is running, which would cache a slow lookup's \"not found\". Exits non-zero when the " +
			"daemon does not answer or a check fails.",
		args: cobra.NoArgs,
		run: func(ctx context.Context, root *RootCommand, cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			failed := false

			resp, err := server.CallAdmin(ctx, root.adminSocket, server.AdminRequest{Op: server.AdminStatus})
			if err != nil {
				fmt.Fprintf(out, "daemon:        not answering (%v)\n", err)
				failed = true
			} else {
				printDaemonStatus(out, resp.Status)
			}

			report := server.CheckNSSwitch(root.env.NSSwitch)
			if report.OK() {
				fmt.Fprintf(out, "nsswitch.conf: ok (systemd on passwd, group and shadow)\n")
			} else {
				failed = true
				fmt.Fprintf(out, "nsswitch.conf: FAILED\n")
				for _, p := range report.Problems() {
					fmt.Fprintf(out, "  %s\n", p)
				}
			}
			if caches := server.RunningCaches(root.env.Proc); len(caches) > 0 {
				fmt.Fprintf(out, "warning:       %s is running; it must not cache passwd or group, or it caches a slow lookup's \"not found\"\n",
					strings.Join(caches, " and "))
			}
			if failed {
				return errStatusFailed
			}
			return nil
		},
	}
}

// printDaemonStatus writes the running daemon's part of status.
func printDaemonStatus(out io.Writer, s *userdb.Status) {
	if s == nil {
		// not covered: a status answer always carries a status.
		return
	}
	up := s.Upstream
	fmt.Fprintf(out, "state:         %s\n", up.State)
	fmt.Fprintf(out, "server:        %s\n", orDash(up.Server))
	fmt.Fprintf(out, "key id:        %s\n", orDash(up.KeyID))
	if !up.RetryAt.IsZero() {
		fmt.Fprintf(out, "retry at:      %s\n", up.RetryAt.Format(time.RFC3339))
	}
	if up.LastError != "" {
		fmt.Fprintf(out, "last error:    %s (%s)\n", up.LastError, up.LastErrorAt.Format(time.RFC3339))
	}
	fmt.Fprintf(out, "cache:         %s, %d users (%d stale), %d groups (%d stale), %d remembered misses\n",
		s.CacheMode, s.Cache.Users, s.Cache.StaleUsers, s.Cache.Groups, s.Cache.StaleGroups, s.NegativeEntries)
	if s.CacheError != "" {
		fmt.Fprintf(out, "cache error:   %s\n", s.CacheError)
	}
	fmt.Fprintf(out, "overrides:     %d\n", s.Overrides)
	fmt.Fprintf(out, "started:       %s\n", s.StartedAt.Format(time.RFC3339))
}
