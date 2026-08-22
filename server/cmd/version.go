package cmd

import (
	"context"
	"fmt"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/version"
)

var _ simplecobra.Commander = (*versionCommand)(nil)

// versionCommand prints build/version info. Deliberately not a simple
// leaf command — this must work even when Bootstrap fails, so it cannot
// check root's init errors like other commands do.
type versionCommand struct{}

func newVersionCommand() simplecobra.Commander { return &versionCommand{} }

// Name implements simplecobra.Commander.
func (c *versionCommand) Name() string { return "version" }

// Commands implements simplecobra.Commander.
func (c *versionCommand) Commands() []simplecobra.Commander { return nil }

// Init implements simplecobra.Commander.
func (c *versionCommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Print ssoosshd version, commit, and build info."
	return nil
}

// PreRun implements simplecobra.Commander. Unlike other commands, version
// must not fail if root init fails — it's the diagnostic a bug reporter
// runs precisely when the server won't start.
func (c *versionCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	return nil
}

// Run implements simplecobra.Commander.
func (c *versionCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	// "ssoosshd", not version.Name ("ssoossh") — that field is the shared
	// project identifier used for logging/observability tags across client,
	// server, and pam, not this binary's name.
	fmt.Printf("ssoosshd %s (commit %s, built %s by %s)\n", version.Version, version.Commit, version.Date, version.BuiltBy)
	return nil
}
