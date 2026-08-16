package cmd

import (
	"context"
	"fmt"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/version"
)

var _ simplecobra.Commander = (*versionCommand)(nil)

// versionCommand prints build/version info. Deliberately not a
// *simpleCommand: that always checks the root's InitErr before running
// (simplecommand.go), but a diagnostic command has to keep working when
// config, the API client, or the SSH agent failed to initialize -- that is
// exactly the situation a bug reporter needs it in.
type versionCommand struct{}

func newVersionCommand() simplecobra.Commander { return &versionCommand{} }

// Name implements simplecobra.Commander.
func (c *versionCommand) Name() string { return "version" }

// Commands implements simplecobra.Commander.
func (c *versionCommand) Commands() []simplecobra.Commander { return nil }

// Init implements simplecobra.Commander.
func (c *versionCommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Print ssoossh version, commit, and build info."
	return nil
}

// PreRun implements simplecobra.Commander. Root's PreRun still runs first
// (simplecobra calls it on every ancestor unconditionally), but its result
// is ignored here rather than checked, unlike simpleCommand's Run.
func (c *versionCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander.
func (c *versionCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	fmt.Printf("%s %s (commit %s, built %s by %s)\n", version.Name, version.Version, version.Commit, version.Date, version.BuiltBy)
	return nil
}
