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

	"github.com/spf13/cobra/doc"

	clientcmd "github.com/mnestor/ssoossh/client/cmd"
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

// generateClientManpage generates the client pages from the client's real
// command tree. date is threaded through from run so both trees carry the
// same stamp; see manPageDate for why it is not time.Now().
//
// This used to hand-build a parallel cobra tree, on the reasoning that
// simplecobra put the assembled tree out of reach. The cost was a gate that
// reported success while measuring nothing: `make man-check` regenerates
// and diffs, so against a hand-built tree it compared the duplicate to
// itself and always passed. The page drifted until it described `host` as
// "Manage host certificates" — a feature this product does not have
// (docs/project/decisions.md) — and omitted --verbose along with every subcommand
// below the top
// level. clientcmd.CobraCommandForManpage returns the same tree Execute
// runs, so the diff means something again.
func generateClientManpage(outDir string, date time.Time) error {
	root, err := clientcmd.CobraCommandForManpage()
	if err != nil {
		return fmt.Errorf("build client command tree: %w", err)
	}

	return doc.GenManTree(root, &doc.GenManHeader{
		Title:   "SSOOSSH",
		Section: "1",
		Source:  "ssoossh",
		Date:    &date,
	}, outDir)
}
