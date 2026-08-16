package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/bep/simplecobra"
)

func newSSHConfigCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "config",
		short: "Print or validate the effective ssoossh client configuration.",
		long: "Prints the configuration as it was actually resolved — after merging every " +
			"config file, applying defaults, and deciding the key algorithm — rather than " +
			"the contents of any one file. Use it to answer \"which config is this picking " +
			"up, and what will it generate?\" without running a login.\n\n" +
			"Loading the configuration is itself the validation: an unusable combination " +
			"fails before this command prints anything.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return runConfig(root, cd.CobraCommand.OutOrStdout())
		},
	}
}

// runConfig prints the resolved configuration. Reaching here at all means the
// config loaded and the key settings resolved — simpleCommand.Run fails on
// root.InitErr() first — so this reports rather than re-validates.
func runConfig(root *RootCommand, out io.Writer) error {
	cfg := root.Config()

	// The key algorithm and size are the interesting half: neither is
	// necessarily what the file says, since both have defaults that depend on
	// whether FIPS mode is in effect. Warnings are not reprinted here; config
	// loading emits them once, to the log.
	algorithm, size, _, err := cfg.ResolveSSHKey()
	if err != nil {
		return fmt.Errorf("invalid ssh key configuration: %w", err)
	}

	keyDescription := algorithm
	if size > 0 {
		keyDescription = fmt.Sprintf("%s (%d)", algorithm, size)
	}

	fmt.Fprintf(out, "%-22s %s\n", "Server", orNone(cfg.Server))
	fmt.Fprintf(out, "%-22s %s\n", "TLS verification", enabledDisabled(!cfg.SkipVerifySSL))
	fmt.Fprintf(out, "%-22s %s\n", "Key type", keyDescription)
	fmt.Fprintf(out, "%-22s %s\n", "FIPS steering", enabledDisabled(cfg.FIPSEnabled()))
	fmt.Fprintf(out, "%-22s %s\n", "Storage", storageDescription(root))
	fmt.Fprintf(out, "%-22s %s\n", "Key file", orNone(cfg.Filename))
	fmt.Fprintf(out, "%-22s %s\n", "Open browser", enabledDisabled(cfg.TryOpenBrowser))
	fmt.Fprintf(out, "%-22s %s\n", "CA public key", orNone(caSummary(cfg.CAPubkey)))

	return nil
}

// storageDescription reports where keys actually end up, which is a runtime
// answer rather than a configured one: `use_agent` and `fallback_file_agent`
// state a preference, and whether an agent was reachable settles it.
func storageDescription(root *RootCommand) string {
	agent := root.Agent()
	if agent == nil {
		return "(not initialized)"
	}
	return fmt.Sprintf("%s (%s)", agent.Type(), agent.Backend())
}

// caSummary shortens the CA public key to its comment-free key material,
// truncated: the full base64 blob is several lines of terminal noise and
// nobody reads it, but enough of it to compare two deployments is useful.
func caSummary(ca string) string {
	if ca == "" {
		return ""
	}
	const shown = 24
	fields := splitFields(ca)
	if len(fields) < 2 {
		return truncate(ca, shown)
	}
	return fields[0] + " " + truncate(fields[1], shown)
}

// splitFields splits on whitespace without pulling in strings.Fields's
// allocation for the common two-field case.
func splitFields(s string) []string {
	var fields []string
	start := -1
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if start >= 0 {
				fields = append(fields, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		fields = append(fields, s[start:])
	}
	return fields
}

// truncate shortens s to at most n runes, marking that it was shortened.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// enabledDisabled renders a boolean setting the way a reader of this output
// thinks about it.
func enabledDisabled(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled"
}
