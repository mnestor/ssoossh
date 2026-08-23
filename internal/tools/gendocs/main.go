// Command gendocs generates man pages from cobra commands.
// Usage: gendocs <output-dir>
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	servercmd "github.com/mnestor/ssoossh/server/cmd"
)

// manPageDate returns the date stamped into every generated page's HISTORY
// line.
//
// Fixed rather than time.Now(), which is what cobra uses when the header
// leaves Date nil. A current date makes the output differ on every run, so
// `make man-check` -- which asserts the committed pages still match what the
// commands produce -- could never pass on any day but the one the pages were
// last generated. Bump this deliberately when the documentation changes in a
// way worth dating.
//
// SOURCE_DATE_EPOCH overrides it, following the reproducible-builds
// convention, so a downstream rebuild can stamp its own date.
func manPageDate() (time.Time, error) {
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		secs, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse SOURCE_DATE_EPOCH: %w", err)
		}
		return time.Unix(secs, 0).UTC(), nil
	}
	return time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC), nil
}

// run performs the actual man page generation. It is extracted from main()
// to enable testing. It returns an error instead of calling os.Exit or log.Fatalf.
func run(outDir string) error {
	//nolint:gosec // G703: path traversal is intentional - outDir is from command-line argument
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Generate server (ssoosshd) man page from the Command wrapper
	serverCmd := servercmd.NewCommand()
	cobraCmd := serverCmd.Command()
	// not covered: a defensive check that cannot trip, because
	// servercmd.NewCommand always returns a wrapper holding a non-nil
	// cobra.Command.
	if cobraCmd == nil {
		return fmt.Errorf("get cobra command from server Command wrapper")
	}

	date, err := manPageDate()
	if err != nil {
		return err
	}

	err = doc.GenManTree(cobraCmd, &doc.GenManHeader{
		Title:   "SSOOSSHD",
		Section: "8",
		Source:  "ssoosshd",
		Date:    &date,
	}, outDir)
	if err != nil {
		return fmt.Errorf("generate server man page: %w", err)
	}
	fmt.Printf("Generated %s\n", filepath.Join(outDir, "ssoosshd.8"))

	// Generate client (ssoossh) man page - create a minimal cobra tree just for docs
	err = generateClientManpage(outDir, date)
	if err != nil {
		return fmt.Errorf("generate client man page: %w", err)
	}
	fmt.Printf("Generated %s\n", filepath.Join(outDir, "ssoossh.1"))

	return nil
}

// main() parses command-line arguments and calls run().
//
// not covered: both failure paths terminate the process (os.Exit and
// log.Fatal), which a Go test cannot survive without re-executing the
// binary through os/exec. The testable logic lives in run().
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: gendocs <output-dir>\n")
		os.Exit(1)
	}

	if err := run(os.Args[1]); err != nil {
		log.Fatal(err)
	}
}

// generateClientManpage creates a minimal client command tree for man page
// generation. date is threaded through from run so both trees carry the same
// stamp; see manPageDate for why it is not time.Now().
func generateClientManpage(outDir string, date time.Time) error {
	// Create a minimal root command that represents ssoossh
	// We can't generate from the real client command tree easily due to simplecobra's design,
	// so we create a minimal representation for documentation purposes.
	root := &cobra.Command{
		Use:   "ssoossh",
		Short: "The ssoossh client — turns an OIDC login into a short-lived SSH certificate, from your ssh_config.",
		Long: "The ssoossh client wires SSO into your existing SSH workflow. Configured " +
			"as a ProxyCommand or Match exec in ssh_config, it generates a fresh keypair, " +
			"hands the public key to the ssoossh server, opens your browser for OIDC " +
			"authentication, and loads the signed certificate into your ssh-agent — or writes " +
			"key and certificate files when no agent is available. Private keys never leave " +
			"the machine. Valid certificates are reused until they expire, so authenticating " +
			"once could cover a workday rather than every connection. Runs on macOS, Linux, " +
			"and Windows, and also handles host enrollment, per-host principal mapping for " +
			"AuthorizedPrincipalsCommand, and service-account certificates for unattended " +
			"jobs.",
	}

	root.PersistentFlags().StringP("config", "c", "", "path to the ssoossh config file")
	root.PersistentFlags().String("server", "", "server address including scheme (e.g. \"https://example.com\") assumes https if omitted.")

	// Add minimal subcommands to match the client structure
	root.AddCommand(&cobra.Command{
		Use:   "ssh",
		Short: "Manage SSH certificates",
	})
	root.AddCommand(&cobra.Command{
		Use:   "host",
		Short: "Manage host certificates",
	})
	root.AddCommand(&cobra.Command{
		Use:   "service",
		Short: "Manage service certificates",
	})
	root.AddCommand(&cobra.Command{
		Use:   "ca",
		Short: "Manage CA certificates",
	})
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print ssoossh version, commit, and build info.",
	})

	return doc.GenManTree(root, &doc.GenManHeader{
		Title:   "SSOOSSH",
		Section: "1",
		Source:  "ssoossh",
		Date:    &date,
	}, outDir)
}
