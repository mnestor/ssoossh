package cmd

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/version"
)

// debugFlagName is the hidden flag, and debugEnvVar its environment
// equivalent. The env var exists because the places this is hardest to
// diagnose are the ones whose command line you do not control at the
// moment you need it: an ssh_config `Match exec` line, a cron entry, a
// systemd unit.
const (
	debugFlagName = "debug"
	debugEnvVar   = "SSOOSSH_DEBUG"
)

// debugEnabled reports whether the diagnostic report was asked for, by
// either route. A malformed env value counts as off rather than as an
// error: this is a diagnostic aid, and failing a login over it would be
// absurd.
func debugEnabled(cmd *cobra.Command) bool {
	if cmd != nil {
		if on, err := cmd.Flags().GetBool(debugFlagName); err == nil && on {
			return true
		}
	}
	on, err := strconv.ParseBool(os.Getenv(debugEnvVar))
	return err == nil && on
}

// writeDebugReport prints what this invocation actually resolved: which
// configuration sources were consulted and what came of each, the settings
// that resulted, and where keys and certificates will be read and written.
//
// Written to stderr, never stdout: stdout carries certificates
// (`service retrieve`), relayed connection data (`ssh proxycommand`), and
// the principal list sshd parses (`host principals`), none of which
// tolerate commentary.
//
// Deliberately printed even when initialization failed. A failed startup is
// when this is most wanted, and the sources list is populated before most
// of the things that can fail.
func writeDebugReport(out io.Writer, cfg *config.Config, ssh agentDescriber, commandPath string, initErr error) {
	fmt.Fprintf(out, "=== ssoossh debug ===\n")
	fmt.Fprintf(out, "%-20s %s %s (%s/%s)\n", "version", version.Name, version.Version, runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(out, "%-20s %s\n", "command", commandPath)
	if wd, err := os.Getwd(); err == nil {
		// The working directory decides where the "local file" source below
		// is looked for, so a surprising entry there usually has its
		// explanation here.
		fmt.Fprintf(out, "%-20s %s\n", "working dir", wd)
	}

	if initErr != nil {
		fmt.Fprintf(out, "%-20s %s\n", "init error", initErr)
	}

	if cfg == nil {
		fmt.Fprintf(out, "\nconfiguration never loaded; nothing further to report\n")
		return
	}

	writeDebugSources(out, cfg.Sources)
	writeDebugSettings(out, cfg, ssh)
	writeDebugStorage(out, cfg, ssh)
}

// writeDebugSources prints the merge chain in application order. Later
// entries win, which is why the order is preserved rather than sorted.
func writeDebugSources(out io.Writer, sources []config.ConfigSource) {
	fmt.Fprintf(out, "\nconfig sources, applied in order — each overrides the ones above it\n")
	if len(sources) == 0 {
		fmt.Fprintf(out, "  (none recorded)\n")
		return
	}

	anyLocked := false
	for i, s := range sources {
		target := s.Label
		if s.Path != "" {
			target = fmt.Sprintf("%s  %s", s.Label, s.Path)
		}
		lock := "  "
		if s.AdminLock {
			// Marked rather than left to the reader: "last one wins"
			// explains the mechanism, not that these two are locks a user
			// cannot override no matter what they put in their own file.
			lock = " *"
			anyLocked = true
		}
		line := fmt.Sprintf(" %2d%s %-56s %s", i+1, lock, target, s.Status)
		if s.Status == config.SourceError {
			// The case this whole report exists for: a file that is present
			// and believed to be in effect, but was skipped.
			line += ": " + s.Err
		}
		if s.Detail != "" {
			line += ": " + s.Detail
		}
		fmt.Fprintln(out, line)
	}

	if anyLocked {
		fmt.Fprintf(out, "\n  * administrator lock — overrides everything above it, including your\n")
		fmt.Fprintf(out, "    own config file and any command-line flag.\n")
	}
}

// writeDebugSettings prints the settings that came out of the merge. This
// report is the only place they are reported: `ssh config` used to print a
// shorter version of the same thing, and two commands answering "what is in
// effect" with different amounts of truth is a maintenance trap rather than
// a convenience. What surrounds this block here is what made it — the
// sources the values came from, and the files they name.
func writeDebugSettings(out io.Writer, cfg *config.Config, ssh agentDescriber) {
	fmt.Fprintf(out, "\nresolved settings\n")

	// The key algorithm and size are the interesting half: neither is
	// necessarily what the file says, since both have defaults that depend
	// on whether FIPS mode is in effect.
	//
	// Reported rather than returned as an error: ResolveSSHKey already
	// failed the startup if the combination is unusable, so reaching here
	// with an error means something stranger, and hiding it would defeat
	// the point of the report.
	var keyDescription string
	algorithm, size, _, err := cfg.ResolveSSHKey()
	switch {
	case err != nil:
		keyDescription = fmt.Sprintf("unresolvable: %v", err)
	case size > 0:
		keyDescription = fmt.Sprintf("%s (%d)", algorithm, size)
	default:
		keyDescription = algorithm
	}

	for _, row := range [][2]string{
		{"Server", orNone(cfg.Server)},
		{"TLS verification", enabledDisabled(!cfg.SkipVerifySSL)},
		{"Key type", keyDescription},
		{"FIPS steering", enabledDisabled(cfg.FIPSEnabled())},
		{"Storage", storageDescription(ssh)},
		{"Key file", orNone(cfg.Filename)},
		{"Open browser", enabledDisabled(cfg.TryOpenBrowser)},
		{"CA public key", orNone(caSummary(cfg.CAPubkey))},
	} {
		fmt.Fprintf(out, "  %-22s %s\n", row[0], row[1])
	}

	if len(cfg.ForbiddenCertificateExtensions) > 0 {
		fmt.Fprintf(out, "  %-22s %s\n", "Forbidden extensions", strings.Join(cfg.ForbiddenCertificateExtensions, ", "))
	}
}

// storageDescription reports where keys actually end up, which is a runtime
// answer rather than a configured one: `use_agent` and `fallback_file_agent`
// state a preference, and whether an agent was reachable settles it. Nil
// covers both "not resolved yet" and "resolution is what failed", which is
// when this report matters most.
func storageDescription(ssh agentDescriber) string {
	if ssh == nil {
		return "(not initialized)"
	}
	return fmt.Sprintf("%s (%s)", ssh.Type(), ssh.Backend())
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

// agentDescriber is the part of agent.Agent this report needs. Narrow on
// purpose: the report must work when agent resolution is exactly what
// failed, so it takes the smallest thing that can answer and tolerates nil.
type agentDescriber interface {
	Type() string
	Backend() string
}

// writeDebugStorage prints where keys and certificates live, and what the
// two settings that govern that actually resolved to at runtime. The
// preference and the outcome are both shown because the interesting bug is
// when they disagree — use_agent true with a file backend means the agent
// was unreachable and fallback_file_agent caught it. The outcome is also the
// settings block's "Storage" line; it is repeated here because the disagreement
// is only visible when the two sit next to each other.
func writeDebugStorage(out io.Writer, cfg *config.Config, ssh agentDescriber) {
	fmt.Fprintf(out, "\nkey storage\n")
	fmt.Fprintf(out, "  %-22s %t\n", "use_agent", cfg.UseAgent)
	fmt.Fprintf(out, "  %-22s %t\n", "fallback_file_agent", cfg.FallbackFileAgent)
	fmt.Fprintf(out, "  %-22s %s\n", "resolved backend", storageDescription(ssh))
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		fmt.Fprintf(out, "  %-22s %s\n", "SSH_AUTH_SOCK", sock)
	} else {
		fmt.Fprintf(out, "  %-22s unset\n", "SSH_AUTH_SOCK")
	}

	if cfg.Filename == "" {
		fmt.Fprintf(out, "  %-22s %s\n", "key_filename", "(none)")
		return
	}

	// Resolved the way the file agent resolves it, not echoed back. A bare
	// name and a "~/..." path both mean something other than what they look
	// like (see agent.ResolveKeyPath), so printing the configured string
	// and stat'ing it would report files as missing that the agent is
	// using perfectly well.
	fmt.Fprintf(out, "  %-22s %s\n", "key_filename", cfg.Filename)
	resolved, err := agent.ResolveKeyPath(cfg.Filename)
	if err != nil {
		fmt.Fprintf(out, "  %-22s unresolvable: %v\n", "resolves to", err)
		return
	}
	if resolved != cfg.Filename {
		fmt.Fprintf(out, "  %-22s %s\n", "resolves to", resolved)
	}

	writeDebugKeyFile(out, "private key", resolved)
	writeDebugKeyFile(out, "public key", publicKeyPathFor(resolved))
	writeDebugKeyFile(out, "certificate", certificatePathFor(resolved))
}

// writeDebugKeyFile prints one of the three key files and whether it is
// there. path is already resolved by the caller.
func writeDebugKeyFile(out io.Writer, label, path string) {
	fmt.Fprintf(out, "  %-22s %s %s\n", label, path, fileState(path))
}

// fileState says whether a path is there, for the file list above. Any
// error other than "not there" is shown as-is: a permission problem on a
// key file is a real answer to "why can this not read my key", and
// flattening it to "missing" would send the reader looking in the wrong
// place.
func fileState(path string) string {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		return fmt.Sprintf("(exists, %d bytes, mode %o)", info.Size(), info.Mode().Perm())
	case os.IsNotExist(err):
		return "(missing)"
	default:
		return fmt.Sprintf("(unreadable: %v)", err)
	}
}
