package cmd

import (
	"context"
	"errors"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/configcheck"
	"github.com/mnestor/ssoossh/nssd/config"
)

// newConfigCommand is `ssoossh-nss config`.
func newConfigCommand(root *RootCommand) *leaf {
	return &leaf{
		root:     root,
		name:     "config",
		short:    "Configuration tools.",
		long:     "Tools that work on nss.yaml and the files it names without starting the daemon.",
		commands: []simplecobra.Commander{newConfigCheckCommand(root)},
	}
}

// newConfigCheckCommand is `ssoossh-nss config check`.
func newConfigCheckCommand(root *RootCommand) *leaf {
	var asJSON bool
	return &leaf{
		root:  root,
		name:  "check",
		short: "Check nss.yaml, the override file and the fleet key without starting the daemon.",
		long: "Reads what the daemon reads before it opens anything, through the same code: nss.yaml " +
			"(--config), the host override file (--overrides) and the fleet key file nss.yaml names, " +
			"with the same mode and owner checks (no group or other access to nss.yaml and the key, no " +
			"group or other write to the override file, and, when run as root, owned by root). It " +
			"reports every problem rather than the first, then the warnings a start logs, including the " +
			"nsswitch.conf check.\n\n" +
			"Nothing is contacted: not the server, the running daemon or the cache. The key's secret " +
			"is read but never printed. Run it as root, as the daemon runs, for the owner checks to " +
			"apply.\n\n" +
			"Exit status: 0 when the daemon would start, warnings included; 1 when it would refuse " +
			"to, or when the command itself fails.",
		args: cobra.NoArgs,
		flags: func(cmd *cobra.Command) {
			cmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
		},
		run: func(_ context.Context, root *RootCommand, cmd *cobra.Command, _ []string) error {
			report := root.checkConfig()
			write := report.WriteText
			if asJSON {
				write = report.WriteJSON
			}
			if err := write(cmd.OutOrStdout()); err != nil {
				return err
			}
			return report.Err()
		},
	}
}

// checkConfig builds the report from config.LoadStartup, the call the
// daemon starts with, and the daemon's own start-time warnings.
func (r *RootCommand) checkConfig() *configcheck.Report {
	report := configcheck.New(r.configPath)
	cfg, err := config.LoadStartup(r.configPath, r.overrides)
	for _, fileErr := range configcheck.Split(err) {
		file := ""
		var fe *config.FileError
		if errors.As(fileErr, &fe) {
			file = fe.File
		}
		kind := configcheck.KindLoad
		switch {
		case errors.Is(fileErr, config.ErrInsecureFile):
			kind = configcheck.KindFilePermissions
		case errors.Is(fileErr, config.ErrInvalid):
			kind = configcheck.KindValidation
		}
		// nss.yaml's validation failures are joined; list each one.
		for _, leafErr := range configcheck.Split(fileErr) {
			finding := configcheck.Finding{Severity: configcheck.SeverityError, Kind: kind, Message: leafErr.Error()}
			if file != r.configPath {
				finding.File = file
			}
			report.Add(finding)
		}
	}

	if cfg != nil {
		for _, w := range cfg.Warnings() {
			report.Add(configcheck.Finding{Severity: configcheck.SeverityWarning, Kind: configcheck.KindWarning, Message: w})
		}
	}
	for _, problem := range r.nsswitchProblems() {
		report.Add(configcheck.Finding{
			Severity: configcheck.SeverityWarning, Kind: configcheck.KindWarning,
			File: r.env.NSSwitch, Message: nsswitchProblemMsg + ": " + problem,
		})
	}
	return report
}
