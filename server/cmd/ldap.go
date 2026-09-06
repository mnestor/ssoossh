package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/service"
)

var (
	_ simplecobra.Commander = (*ldapCommand)(nil)
	_ simplecobra.Commander = (*ldapProbeCommand)(nil)
)

// ldapCommand groups the directory subcommands.
type ldapCommand struct {
	commands []simplecobra.Commander
}

func newLDAPCommand() simplecobra.Commander {
	return &ldapCommand{commands: []simplecobra.Commander{newLDAPProbeCommand()}}
}

// Name implements simplecobra.Commander.
func (c *ldapCommand) Name() string { return "ldap" }

// Commands implements simplecobra.Commander.
func (c *ldapCommand) Commands() []simplecobra.Commander { return c.commands }

// Init implements simplecobra.Commander.
func (c *ldapCommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Directory tools."
	cd.CobraCommand.Long = "Tools for the optional LDAP directory enrichment. " +
		"Everything here is read-only and works against the loaded configuration file."
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *ldapCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander. The bare `ldap` command prints help.
func (c *ldapCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return cd.CobraCommand.Help()
}

// ldapProbeCommand runs one read-only directory lookup from the command
// line.
//
// The same entry point the admin console calls, reached without the server
// running: it answers "will this filter find the right entry" before a
// restart rather than after one, and it has no HTTP surface of its own to
// secure. Whoever can run this can already read the config file, and so
// already has the bind password.
type ldapProbeCommand struct {
	username   string
	email      string
	subject    string
	extra      []string
	filter     string
	literal    bool
	attributes []string
	asJSON     bool
}

func newLDAPProbeCommand() simplecobra.Commander { return &ldapProbeCommand{} }

// Name implements simplecobra.Commander.
func (c *ldapProbeCommand) Name() string { return "probe" }

// Commands implements simplecobra.Commander.
func (c *ldapProbeCommand) Commands() []simplecobra.Commander { return nil }

// Init implements simplecobra.Commander.
func (c *ldapProbeCommand) Init(cd *simplecobra.Commandeer) error {
	cmd := cd.CobraCommand
	cmd.Short = "Look up one entry and report what the configuration would make of it."
	cmd.Long = "Runs the login path's directory lookup and field resolution against the " +
		"configured server, and stops before the write. It reports the filter it actually " +
		"sent, every attribute the directory returned rather than only the ones the " +
		"configuration names, what each configured field resolved to, and which group values " +
		"the allowlist would discard.\n\n" +
		"Nothing is written: no directory record, no group rows, no miss window, no " +
		"auto-disable. The database is not opened at all.\n\n" +
		"The connection comes from the config file and cannot be overridden here, for the " +
		"same reason it cannot be in the web console: the point is to test the configuration " +
		"you are about to run, not a different one."

	cmd.Flags().StringVar(&c.username, "username", "", "value for {{.Username}} in the filter")
	cmd.Flags().StringVar(&c.email, "email", "", "value for {{.Email}} in the filter")
	cmd.Flags().StringVar(&c.subject, "subject", "", "value for {{.Subject}} in the filter")
	cmd.Flags().StringArrayVar(&c.extra, "extra", nil, "value for {{.Extra.<name>}}, as name=value (repeatable)")
	cmd.Flags().StringVar(&c.filter, "filter", "", "filter to run; empty uses the configured ldap.user_filter")
	cmd.Flags().BoolVar(&c.literal, "literal", false, "send --filter exactly as typed, interpolating and escaping nothing")
	cmd.Flags().StringSliceVar(&c.attributes, "attributes", nil, "attributes to request; empty asks for every user attribute")
	cmd.Flags().BoolVar(&c.asJSON, "json", false, "print the result as JSON")
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *ldapProbeCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander.
func (c *ldapProbeCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	cfg, err := config.NewConfig(cd.CobraCommand)
	if err != nil {
		return err
	}
	if !cfg.LDAP.Enabled {
		return fmt.Errorf("ldap.enabled is false in this configuration, so there is nothing to probe")
	}

	extra, err := parseExtraBindings(c.extra)
	if err != nil {
		return err
	}

	// A nil database: the probe reads and writes none. Constructing the
	// service still parses every filter template, so a bad one fails here
	// with the same message the next restart would produce.
	svc, err := service.NewLDAPService(cfg, nil)
	if err != nil {
		return err
	}

	mode := service.ProbeFilterTemplate
	if c.literal {
		mode = service.ProbeFilterLiteral
	}

	result, err := svc.Probe(ctx, service.ProbeRequest{
		Bindings: service.ProbeBindings{
			Username: c.username,
			Email:    c.email,
			Subject:  c.subject,
			Extra:    extra,
		},
		Mode:       mode,
		Filter:     c.filter,
		Attributes: c.attributes,
	})
	if err != nil {
		return err
	}

	out := cd.CobraCommand.OutOrStdout()
	if c.asJSON {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	return writeProbeResult(out, result)
}

// parseExtraBindings turns repeated name=value flags into the map a filter
// template renders {{.Extra.<name>}} against.
func parseExtraBindings(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		name, value, found := strings.Cut(pair, "=")
		if !found || name == "" {
			return nil, fmt.Errorf("--extra %q is not name=value", pair)
		}
		out[name] = value
	}
	return out, nil
}

// writeProbeResult prints the probe in the three stages the login path runs,
// because "is the entry there" and "will the right field land in the right
// place" are different questions and they are hit in that order.
func writeProbeResult(out io.Writer, result *service.ProbeResult) error {
	w := &probeWriter{out: out}

	writeProbeSummary(w, result)
	if result.Entry == nil {
		w.printf("\nThe search succeeded and found nothing. That is what a login would see.\n")
		return w.err
	}

	writeProbeEntry(w, result.Entry)
	writeProbeFields(w, result.Fields)
	writeProbeMerge(w, result.Merge)
	writeProbeSuggestions(w, result.Suggestions)

	return w.err
}

// writeProbeSummary prints the request as it went out and the top-level
// counts and warnings.
func writeProbeSummary(w *probeWriter, result *service.ProbeResult) {
	w.printf("base dn   %s\n", result.BaseDN)
	w.printf("filter    %s", result.FilterSent)
	if result.Mode == service.ProbeFilterTemplate {
		w.printf("   (rendered and RFC 4515 escaped)")
	} else {
		w.printf("   (sent verbatim)")
	}
	w.printf("\nrequested %s\n", strings.Join(result.Attributes, ", "))
	w.printf("matched   %d entr%s in %s\n", result.Matched, plural(result.Matched, "y", "ies"), result.Elapsed.Round(1e6))
	if result.TLSInsecureSkipVerify {
		w.printf("warning   the directory certificate was not verified (ldap.tls_insecure_skip_verify)\n")
	}
	if result.Matched > 1 {
		w.printf("warning   the login path refuses anything but exactly one match; this filter is too loose\n")
	}
}

// writeProbeEntry prints the entry as the directory returned it, marking the
// attributes the current configuration names.
func writeProbeEntry(w *probeWriter, entry *service.ProbeEntry) {
	w.printf("\nentry as returned\n  dn %s\n", entry.DN)
	for _, attr := range entry.Attributes {
		marker := " "
		if attr.Configured {
			marker = "*"
		}
		w.printf("  %s %-28s %s\n", marker, attr.Name, strings.Join(attr.Values, ", "))
		if attr.TruncatedValues > 0 {
			w.printf("    %-28s ...%d more value(s) not shown\n", "", attr.TruncatedValues)
		}
	}
	w.printf("  (* is named by the current configuration)\n")
}

// writeProbeFields prints the field-mapping stage: what each configured field
// resolved to, including any secondary searches.
func writeProbeFields(w *probeWriter, fields []service.ProbeField) {
	w.printf("\nfield mapping\n")
	for _, field := range fields {
		w.printf("  %s\n", field.Name)
		if field.Attribute != "" {
			present := "present"
			if !field.AttributePresent {
				present = "NOT on the entry"
			}
			w.printf("    attribute %s (%s) -> %d value(s)\n", field.Attribute, present, len(field.AttributeValues))
		}
		for _, search := range field.Searches {
			w.printf("    search %s -> %d entr%s\n", search.Name, search.Entries, plural(search.Entries, "y", "ies"))
			w.printf("      %s\n", search.FilterSent)
			if search.Error != "" {
				w.printf("      error: %s\n", search.Error)
			}
		}
		w.printf("    resolved: %s\n", orDash(field.Values))
		if field.Error != "" {
			w.printf("    error: %s\n", field.Error)
		}
	}
}

// writeProbeMerge prints the merge-and-allowlist stage: what the login path
// would keep and drop for each field.
func writeProbeMerge(w *probeWriter, merges []service.ProbeMerge) {
	w.printf("\nmerge and allowlist\n")
	for _, merge := range merges {
		w.printf("  %s (%s)\n", merge.Name, merge.Action)
		w.printf("    kept:    %s\n", orDash(merge.Kept))
		if len(merge.Dropped) > 0 {
			w.printf("    dropped: %s\n", strings.Join(merge.Dropped, ", "))
		}
	}
	w.printf("  Nothing was written.\n")
}

// writeProbeSuggestions prints the config lines that would map what the probe
// currently ignores, when there are any.
func writeProbeSuggestions(w *probeWriter, suggestions []service.ProbeSuggestion) {
	if len(suggestions) == 0 {
		return
	}
	w.printf("\nconfig that would keep what is being ignored\n")
	for _, suggestion := range suggestions {
		w.printf("  # %s\n", suggestion.Reason)
		for _, line := range strings.Split(suggestion.YAML, "\n") {
			w.printf("  %s\n", line)
		}
		w.printf("\n")
	}
}

// probeWriter collapses the error handling of a long report into one check
// at the end: a failed write to stdout is worth reporting once, not at every
// line.
type probeWriter struct {
	out io.Writer
	err error
}

func (w *probeWriter) printf(format string, args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintf(w.out, format, args...)
}

// plural picks the singular or plural form for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// orDash renders an empty value list as a dash, so an empty field is visibly
// empty rather than a blank line.
func orDash(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	return strings.Join(values, ", ")
}
