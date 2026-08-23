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
	writeDebugSettings(out, cfg)
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

// writeDebugSettings prints the settings that came out of the merge.
func writeDebugSettings(out io.Writer, cfg *config.Config) {
	fmt.Fprintf(out, "\nresolved settings\n")
	fmt.Fprintf(out, "  %-20s %s\n", "server", orNone(cfg.Server))
	fmt.Fprintf(out, "  %-20s %s\n", "tls verification", enabledDisabled(!cfg.SkipVerifySSL))
	fmt.Fprintf(out, "  %-20s %s\n", "fips steering", enabledDisabled(cfg.FIPSEnabled()))
	fmt.Fprintf(out, "  %-20s %s\n", "open browser", enabledDisabled(cfg.TryOpenBrowser))

	// Reported rather than returned as an error: ResolveSSHKey already
	// failed the startup if the combination is unusable, so reaching here
	// with an error means something stranger, and hiding it would defeat
	// the point of the report.
	algorithm, size, _, err := cfg.ResolveSSHKey()
	if err != nil {
		fmt.Fprintf(out, "  %-20s unresolvable: %v\n", "key type", err)
		return
	}
	keyDescription := algorithm
	if size > 0 {
		keyDescription = fmt.Sprintf("%s (%d)", algorithm, size)
	}
	fmt.Fprintf(out, "  %-20s %s\n", "key type", keyDescription)
	if len(cfg.ForbiddenCertificateExtensions) > 0 {
		fmt.Fprintf(out, "  %-20s %s\n", "forbidden exts", strings.Join(cfg.ForbiddenCertificateExtensions, ", "))
	}
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
// was unreachable and fallback_file_agent caught it.
func writeDebugStorage(out io.Writer, cfg *config.Config, ssh agentDescriber) {
	fmt.Fprintf(out, "\nkey storage\n")
	fmt.Fprintf(out, "  %-20s %t\n", "use_agent", cfg.UseAgent)
	fmt.Fprintf(out, "  %-20s %t\n", "fallback_file_agent", cfg.FallbackFileAgent)

	if ssh == nil {
		fmt.Fprintf(out, "  %-20s not resolved\n", "backend")
	} else {
		fmt.Fprintf(out, "  %-20s %s (%s)\n", "backend", ssh.Type(), ssh.Backend())
	}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		fmt.Fprintf(out, "  %-20s %s\n", "SSH_AUTH_SOCK", sock)
	} else {
		fmt.Fprintf(out, "  %-20s unset\n", "SSH_AUTH_SOCK")
	}

	if cfg.Filename == "" {
		fmt.Fprintf(out, "  %-20s %s\n", "key_filename", "(none)")
		return
	}

	// Resolved the way the file agent resolves it, not echoed back. A bare
	// name and a "~/..." path both mean something other than what they look
	// like (see agent.ResolveKeyPath), so printing the configured string
	// and stat'ing it would report files as missing that the agent is
	// using perfectly well.
	fmt.Fprintf(out, "  %-20s %s\n", "key_filename", cfg.Filename)
	resolved, err := agent.ResolveKeyPath(cfg.Filename)
	if err != nil {
		fmt.Fprintf(out, "  %-20s unresolvable: %v\n", "resolves to", err)
		return
	}
	if resolved != cfg.Filename {
		fmt.Fprintf(out, "  %-20s %s\n", "resolves to", resolved)
	}

	writeDebugKeyFile(out, "private key", resolved)
	writeDebugKeyFile(out, "public key", publicKeyPathFor(resolved))
	writeDebugKeyFile(out, "certificate", certificatePathFor(resolved))
}

// writeDebugKeyFile prints one of the three key files and whether it is
// there. path is already resolved by the caller.
func writeDebugKeyFile(out io.Writer, label, path string) {
	fmt.Fprintf(out, "  %-20s %s %s\n", label, path, fileState(path))
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
