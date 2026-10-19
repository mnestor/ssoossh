package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/server"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

// errStatusFailed is status's exit when a check failed or the daemon did
// not answer, after everything it could find out was printed.
var errStatusFailed = errors.New("status: a check failed")

// newStatusCommand is `ssoossh-nss status`.
func newStatusCommand(root *RootCommand) *leaf {
	return &leaf{
		root:  root,
		name:  "status",
		short: "Show the daemon's state and check that this host consults it.",
		long: "Prints the directory connection's state (online, offline, unauthorized or rate limited), the " +
			"server, the trust in force (system store, CA file, key pin, or a trust root and a pin), the fleet key id, " +
			"the cache counts and the last error, from the running daemon, with any " +
			"group name collisions (a directory group with a local group's name and another gid, which is not " +
			"resolved on this host) and refused groups (outside min_id..max_id) met since the start or the last " +
			"SIGHUP; then " +
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
	fmt.Fprintf(out, "trust:         %s\n", describeTrust(up.TLSCAFile, up.TLSPins))
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
	printGroupFindings(out, s.Groups)
	fmt.Fprintf(out, "started:       %s\n", s.StartedAt.Format(time.RFC3339))
}

// describeTrust names what the daemon trusts to be the server, from
// tls_ca_file and the number of tls_spki_pin entries: the system store,
// a CA file, a key pin, or a trust root and a pin, both of which must
// hold. "-" when neither is set, which validation never lets start.
func describeTrust(caFile string, pins int) string {
	var parts []string
	switch caFile {
	case "":
	case config.TLSCASystem:
		parts = append(parts, "system store (any CA this host trusts)")
	default:
		parts = append(parts, "CA file "+caFile)
	}
	if pins > 0 {
		parts = append(parts, fmt.Sprintf("key pin (%d %s)", pins, plural(pins, "pin", "pins")))
	}
	return orDash(strings.Join(parts, " and "))
}

// printGroupFindings writes the refused directory groups, when there are
// any: one line per kind, then one indented line per group.
func printGroupFindings(out io.Writer, f policy.Findings) {
	if n := len(f.Collisions) + f.MoreCollisions; n > 0 {
		fmt.Fprintf(out, "collisions:    %d group name %s (a directory group with a local group's name; not resolved here, rename one of the two)\n",
			n, plural(n, "collision", "collisions"))
		for _, c := range f.Collisions {
			local := "unknown"
			if c.LocalGID != nil {
				local = strconv.FormatUint(uint64(*c.LocalGID), 10)
			}
			fmt.Fprintf(out, "  %s: directory gid %d, local gid %s, first seen %s\n", c.Name, c.GID, local, c.FirstSeen.Format(time.RFC3339))
		}
		printMore(out, f.MoreCollisions)
	}
	if n := len(f.Refused) + f.MoreRefused; n > 0 {
		fmt.Fprintf(out, "refused:       %d refused %s (not served on this host)\n", n, plural(n, "group", "groups"))
		for _, r := range f.Refused {
			fmt.Fprintf(out, "  %s: %s, first seen %s\n", r.Name, r.Reason, r.FirstSeen.Format(time.RFC3339))
		}
		printMore(out, f.MoreRefused)
	}
}

// printMore writes the count past the listed entries.
func printMore(out io.Writer, more int) {
	if more > 0 {
		fmt.Fprintf(out, "  and %d more\n", more)
	}
}

// plural picks the word for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
