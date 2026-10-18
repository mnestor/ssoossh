// Command e2ehelper is the test-only helper for the NSS end-to-end job
// (test/nss/e2e, scripts/nss-e2e.sh), which runs the real ssoosshd against
// a throwaway OpenLDAP and the real ssoosshd-nss behind nss-systemd. It
// does the parts of that job no shipped binary does:
//
//	setup     write the server's TLS certificate and key, its SPKI pin and
//	          an SSH CA key into a directory
//	oidc      serve a loopback OIDC provider that signs in whoever submits
//	          its form, so ssoosshd can start and a person can log in
//	login     sign a person in to ssoosshd through that provider
//	seed-key  write a fleet key row and the daemon's key file
//	revoke-key revoke a fleet key in the database
//	linked    report whether an account's posix row is linked to a login
//
// Test code only: nothing here is shipped or reached by a production
// build. The secret seed-key creates is written to the key file and
// nowhere else.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// main dispatches to the subcommand named by the first argument.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2ehelper:", err)
		os.Exit(1)
	}
}

// run runs one subcommand with args, writing its result to out.
func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: e2ehelper setup|oidc|login|seed-key|revoke-key|linked [flags]")
	}
	commands := map[string]func(context.Context, []string, io.Writer) error{
		"setup":      runSetup,
		"oidc":       runOIDC,
		"login":      runLogin,
		"seed-key":   runSeedKey,
		"revoke-key": runRevokeKey,
		"linked":     runLinked,
	}
	command, ok := commands[args[0]]
	if !ok {
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
	return command(ctx, args[1:], out)
}
