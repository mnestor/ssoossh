package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/varlink"
	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

// lookupTimeout bounds one lookup: longer than any answer deadline the
// configuration allows, shorter than systemd's 45 s.
const lookupTimeout = 10 * time.Second

// newLookupCommand is `ssoossh-nss lookup`.
func newLookupCommand(root *RootCommand) *leaf {
	var socket string
	return &leaf{
		root:  root,
		name:  "lookup",
		short: "Ask the userdb socket exactly as nss-systemd does, for debugging.",
		long: "Calls the daemon's userdb socket the way nss-systemd does and prints each reply's JSON. " +
			"userdbctl user NAME and getent passwd NAME exercise the same path through systemd.",
		flags: func(cmd *cobra.Command) {
			cmd.PersistentFlags().StringVar(&socket, "socket", config.UserdbSocket, "the userdb socket")
		},
		commands: []simplecobra.Commander{
			lookupLeaf(root, "user", "GetUserRecord by userName", userdb.MethodGetUserRecord, "userName", false, &socket),
			lookupLeaf(root, "group", "GetGroupRecord by groupName", userdb.MethodGetGroupRecord, "groupName", false, &socket),
			lookupLeaf(root, "memberships", "GetMemberships by userName, streamed", userdb.MethodGetMemberships, "userName", true, &socket),
		},
	}
}

// lookupLeaf is one `lookup` subcommand.
func lookupLeaf(root *RootCommand, name, short, method, key string, more bool, socket *string) *leaf {
	return &leaf{
		root:  root,
		name:  name,
		use:   "NAME",
		short: short + ".",
		args:  cobra.ExactArgs(1),
		run: func(ctx context.Context, _ *RootCommand, cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
			defer cancel()
			c, err := varlink.Dial(ctx, *socket)
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()
			params := map[string]string{key: args[0], "service": userdb.ServiceName}
			out := cmd.OutOrStdout()
			if more {
				err = c.CallMore(ctx, method, params, func(raw json.RawMessage) error { return printJSON(out, raw) })
			} else {
				var raw json.RawMessage
				if err = c.Call(ctx, method, params, &raw); err == nil {
					err = printJSON(out, raw)
				}
			}
			var verr *varlink.Error
			if errors.As(err, &verr) {
				return fmt.Errorf("%s answered %s", *socket, verr.Name)
			}
			return err
		},
	}
}

// printJSON writes raw indented.
func printJSON(w io.Writer, raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		// not covered: the varlink decoder only hands over valid JSON.
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}
