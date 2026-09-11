package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/version"
)

var _ simplecobra.Commander = (*versionCommand)(nil)

// versionCommand prints build/version info. Deliberately not a
// *simpleCommand: that always checks the root's InitErr before running
// (simplecommand.go), but a diagnostic command has to keep working when
// config, the API client, or the SSH agent failed to initialize -- that is
// exactly the situation a bug reporter needs it in.
type versionCommand struct {
	// asJSON selects the machine-readable form. Bound to --json.
	asJSON bool
}

func newVersionCommand() simplecobra.Commander { return &versionCommand{} }

// Name implements simplecobra.Commander.
func (c *versionCommand) Name() string { return "version" }

// Commands implements simplecobra.Commander.
func (c *versionCommand) Commands() []simplecobra.Commander { return nil }

// Offline implements offlineCommander. Every value printed below is baked
// in at build time, so this is the clearest case there is of a command with
// no reason to reach the server. Without it, root's PreRun still fetched
// the CA on the way here, meaning `ssoossh version` opened a connection to
// the configured server on every run purely as a side effect of shared
// init.
func (c *versionCommand) Offline() bool { return true }

// Init implements simplecobra.Commander.
func (c *versionCommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Print ssoossh version, commit, and build info."
	cd.CobraCommand.Long = "Print ssoossh version, commit, and build info.\n\n" +
		"With --json, prints the same values as an object along with a list of the " +
		"command paths this build implements. That list is the supported way for a " +
		"deployment tool to tell releases apart: the command tree has changed shape " +
		"across versions, and probing it by running --help and matching free text " +
		"breaks on wording no release promises to keep."
	cd.CobraCommand.Flags().BoolVar(&c.asJSON, "json", false, "Print version and capability information as JSON.")
	return nil
}

// PreRun implements simplecobra.Commander. Root's PreRun still runs first
// (simplecobra calls it on every ancestor unconditionally), but its result
// is ignored here rather than checked, unlike simpleCommand's Run.
func (c *versionCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander.
func (c *versionCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return writeVersion(os.Stdout, c.asJSON)
}

// versionInfo is the object --json prints. The field names are a contract
// with deployment tooling, so they are spelled explicitly rather than left
// to Go's field names.
type versionInfo struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Commit       string   `json:"commit"`
	Date         string   `json:"date"`
	BuiltBy      string   `json:"built_by"`
	Capabilities []string `json:"capabilities"`
}

// writeVersion renders either form to w. Split from Run so the output can
// be asserted without capturing the process's stdout.
func writeVersion(w io.Writer, asJSON bool) error {
	if !asJSON {
		_, err := fmt.Fprintf(w, "%s %s (commit %s, built %s by %s)\n",
			version.Name, version.Version, version.Commit, version.Date, version.BuiltBy)
		return err
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(versionInfo{
		Name:         version.Name,
		Version:      version.Version,
		Commit:       version.Commit,
		Date:         version.Date,
		BuiltBy:      version.BuiltBy,
		Capabilities: capabilities(),
	}); err != nil {
		return fmt.Errorf("encode version: %w", err)
	}
	return nil
}

// capabilities names every command path this build implements, sorted so
// the output is stable across releases.
//
// Written out rather than walked from the command tree at runtime: the list
// is a promise about what this binary answers to, and a derived list would
// quietly reshape itself around an accidental rename instead of failing.
// TestVersion_CapabilitiesMatchTheCommandTree holds the two together, so a
// command added or renamed without updating this list is a test failure
// rather than a silent contract change.
func capabilities() []string {
	return []string{
		"ca",
		"host mapping add",
		"host mapping list",
		"host mapping remove",
		"host principals",
		"service enroll",
		"service retrieve",
		"ssh config",
		"ssh inspect",
		"ssh login",
		"ssh logout",
		"ssh proxycommand",
		"version",
	}
}
