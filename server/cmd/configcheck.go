package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/configcheck"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/mail"
	"github.com/mnestor/ssoossh/server/service"
)

var (
	_ simplecobra.Commander = (*configCommand)(nil)
	_ simplecobra.Commander = (*configCheckCommand)(nil)
)

// configCommand groups the configuration subcommands.
type configCommand struct {
	commands []simplecobra.Commander
}

// newConfigCommand builds `ssoosshd config`.
func newConfigCommand() simplecobra.Commander {
	return &configCommand{commands: []simplecobra.Commander{newConfigCheckCommand()}}
}

// Name implements simplecobra.Commander.
func (c *configCommand) Name() string { return "config" }

// Commands implements simplecobra.Commander.
func (c *configCommand) Commands() []simplecobra.Commander { return c.commands }

// Init implements simplecobra.Commander.
func (c *configCommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Configuration tools."
	cd.CobraCommand.Long = "Tools that work on the configuration file without starting the server. " +
		"check reports every problem a start would refuse or warn about."
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *configCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander. The bare `config` command prints help.
func (c *configCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return cd.CobraCommand.Help()
}

// configCheckCommand is `ssoosshd config check`: the start's own load and
// validation, run to the end and reported, with nothing opened or
// contacted.
type configCheckCommand struct {
	asJSON      bool
	strict      bool
	showSources bool
}

// newConfigCheckCommand builds `ssoosshd config check`.
func newConfigCheckCommand() simplecobra.Commander { return &configCheckCommand{} }

// Name implements simplecobra.Commander.
func (c *configCheckCommand) Name() string { return "check" }

// Commands implements simplecobra.Commander.
func (c *configCheckCommand) Commands() []simplecobra.Commander { return nil }

// Init implements simplecobra.Commander.
func (c *configCheckCommand) Init(cd *simplecobra.Commandeer) error {
	cmd := cd.CobraCommand
	cmd.Short = "Check the configuration without starting the server."
	cmd.Long = "Loads the configuration exactly as serve and sign do (the same file search " +
		"or --config, defaults.yaml, the ldap.schema preset, then the file) and runs the same " +
		"validation, but reports every problem instead of stopping at the first: settings a " +
		"start refuses, in every section; keys nothing reads, each with the file it is in and " +
		"the closest real key; the TLS key pair, the cert_options templates and policies, and " +
		"the mail templates and relay settings; and the warnings a start logs.\n\n" +
		"Nothing is contacted: no database, directory, broker, CA key source or identity " +
		"provider. Local files the configuration names (the TLS pair, ssh_key_file, password " +
		"files, NATS certificates) are read, as a start reads them. Use ldap probe and the " +
		"admin diagnostics page for the connections.\n\n" +
		"--show-sources adds every effective setting and the layer it came from (default, " +
		"preset or file). Secrets are redacted as on the admin configuration page.\n\n" +
		"Exit status: 0 when the configuration is valid, warnings included; 1 when it has " +
		"any error, when --strict is given and a key is unknown, or when the command itself " +
		"fails."
	cmd.Args = cobra.NoArgs
	cmd.Flags().BoolVar(&c.asJSON, "json", false, "print the report as JSON")
	cmd.Flags().BoolVar(&c.strict, "strict", false, "treat unknown keys as errors")
	cmd.Flags().BoolVar(&c.showSources, "show-sources", false, "list every effective setting and where its value came from")
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *configCheckCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander: print the report, then fail when
// it holds an error so the exit status says so.
func (c *configCheckCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	report := c.check(cd.CobraCommand)
	out := cd.CobraCommand.OutOrStdout()
	write := report.WriteText
	if c.asJSON {
		write = report.WriteJSON
	}
	if err := write(out); err != nil {
		return err
	}
	return report.Err()
}

// check builds the report. The process-wide logger is swapped for a
// recorder while it runs, because some validation logs its warnings as it
// goes rather than returning them; recording them is what keeps this
// report and a start's log from drifting apart.
func (c *configCheckCommand) check(cmd *cobra.Command) *configcheck.Report {
	recorder := configcheck.NewRecorder()
	previous := slog.Default()
	slog.SetDefault(slog.New(recorder))
	defer slog.SetDefault(previous)

	cfg, err := config.Load(cmd)
	if err != nil {
		// The file is the one asked for, if any: when the search found
		// nothing there is no path to name. A flag that cannot be read is
		// the error Load just returned, so it names no file either.
		file, flagErr := cmd.Flags().GetString("config")
		if flagErr != nil {
			file = ""
		}
		report := configcheck.New(file)
		report.Add(configcheck.Finding{Severity: configcheck.SeverityError, Kind: configcheck.KindLoad, Message: err.Error()})
		return report
	}

	report := configcheck.New(cfg.ConfigFile())
	c.addUnknownKeys(report, cfg)
	addValidation(report, cfg)
	for _, w := range cfg.Warnings() {
		report.Add(configcheck.Finding{
			Severity: configcheck.SeverityWarning, Kind: configcheck.KindWarning,
			Section: w.Section, Message: warningText(w.Warning),
		})
	}
	for _, line := range recorder.Lines() {
		report.Add(configcheck.Finding{Severity: configcheck.SeverityWarning, Kind: configcheck.KindWarning, Message: line})
	}

	if c.showSources {
		addSources(report, cfg)
	}
	return report
}

// addUnknownKeys reports each unknown key, a warning unless --strict.
func (c *configCheckCommand) addUnknownKeys(report *configcheck.Report, cfg *config.Config) {
	severity := configcheck.SeverityWarning
	if c.strict {
		severity = configcheck.SeverityError
	}
	for _, k := range cfg.UnknownKeys() {
		report.Add(configcheck.Finding{
			Severity: severity, Kind: configcheck.KindUnknownKey,
			Key: k.Key, File: cfg.ConfigFile(), Suggestion: k.Suggestion,
			Message: "config key is not recognised and has no effect: a typo, or a key from a newer ssoosshd",
		})
	}
}

// addValidation runs the start's Validate and the checks a start makes
// later from the configuration alone: the TLS key pair the HTTP server
// builds, the cert_options the certificate service parses, and the mail
// templates and relay settings the notification handler builds. Each is the
// function the start calls, not a copy of it, and none opens a connection.
// A section Validate already failed is not checked further, as each section
// stops at its own first problem.
func addValidation(report *configcheck.Report, cfg *config.Config) {
	failed := map[string]bool{}
	for _, err := range configcheck.Split(cfg.Validate()) {
		section := ""
		var se *config.SectionError
		if errors.As(err, &se) {
			section = se.Section
		}
		failed[section] = true
		report.Add(configcheck.Finding{Severity: configcheck.SeverityError, Kind: configcheck.KindValidation, Section: section, Message: err.Error()})
	}

	later := []struct {
		section string
		check   func() error
	}{
		{"http", func() error {
			_, _, err := cfg.HTTP.TLS.Build(cfg.FIPSEnabled())
			return err
		}},
		{"cert_options", func() error { return service.CheckCertificateOptions(cfg) }},
		{"mail", func() error { return checkMail(&cfg.Mail) }},
	}
	for _, l := range later {
		if failed[l.section] {
			continue
		}
		if err := l.check(); err != nil {
			report.Add(configcheck.Finding{Severity: configcheck.SeverityError, Kind: configcheck.KindValidation, Section: l.section, Message: err.Error()})
		}
	}
}

// checkMail builds the notification templates and the relay settings as
// bootstrap's newNotificationHandler does, with its error wording, when
// mail is on. Building the SMTP client opens no connection.
func checkMail(m *config.MailConfig) error {
	if !m.Enabled {
		return nil
	}
	if _, err := mail.NewRenderer(m.TemplateDir, m.SubjectPrefix); err != nil {
		return fmt.Errorf("failed to load the notification templates: %w", err)
	}
	if _, err := mail.NewSMTPSender(m); err != nil {
		return fmt.Errorf("failed to configure the mail relay: %w", err)
	}
	return nil
}

// addSources lists every effective setting with its origin. A failure to
// re-read the file is reported rather than dropping the list silently.
func addSources(report *configcheck.Report, cfg *config.Config) {
	sources, err := cfg.EffectiveSources()
	if err != nil {
		report.Add(configcheck.Finding{Severity: configcheck.SeverityError, Kind: configcheck.KindLoad, Message: err.Error()})
		return
	}
	for _, s := range sources {
		report.Sources = append(report.Sources, configcheck.Source{
			Key: s.Key, Value: s.Value, Origin: string(s.Origin), Secret: s.Secret,
		})
	}
}

// warningText renders a config.Warning as one line, its attributes as
// key=value after the message, as the recorder renders a logged one.
func warningText(w config.Warning) string {
	rec := configcheck.NewRecorder()
	slog.New(rec).Warn(w.Msg, w.Attrs...)
	lines := rec.Lines()
	if len(lines) == 0 {
		// not covered: a Recorder records every warning it is handed.
		return w.Msg
	}
	return lines[0]
}
