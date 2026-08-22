// Command gendocs generates man pages from cobra commands.
// Usage: gendocs <output-dir>
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	servercmd "github.com/mnestor/ssoossh/server/cmd"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: gendocs <output-dir>\n")
		os.Exit(1)
	}

	outDir := os.Args[1]
	//nolint:gosec // G703: path traversal is intentional - outDir is from command-line argument
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Generate server (ssoosshd) man page from the Command wrapper
	serverCmd := servercmd.NewCommand()
	cobraCmd := serverCmd.Command()
	if cobraCmd == nil {
		log.Fatal("Failed to get cobra command from server Command wrapper")
	}

	err := doc.GenManTree(cobraCmd, &doc.GenManHeader{
		Title:   "SSOOSSHD",
		Section: "8",
		Source:  "ssoosshd",
	}, outDir)
	if err != nil {
		log.Fatalf("Failed to generate server man page: %v", err)
	}
	fmt.Printf("Generated %s\n", filepath.Join(outDir, "ssoosshd.8"))

	// Generate client (ssoossh) man page - create a minimal cobra tree just for docs
	err = generateClientManpage(outDir)
	if err != nil {
		log.Fatalf("Failed to generate client man page: %v", err)
	}
	fmt.Printf("Generated %s\n", filepath.Join(outDir, "ssoossh.1"))
}

// generateClientManpage creates a minimal client command tree for man page generation.
func generateClientManpage(outDir string) error {
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
	}, outDir)
}
