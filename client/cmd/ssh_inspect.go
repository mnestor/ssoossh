package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bep/simplecobra"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
)

func newSSHInspectCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "inspect",
		short: "Print details of the currently held ssoossh certificate(s).",
		long: "Shows what each certificate ssoossh is holding actually grants — who it is " +
			"for, when it expires, and which extensions and critical options it carries — " +
			"rather than the certificate blob, which tells a human nothing.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return runInspect(root, cd.CobraCommand.OutOrStdout())
		},
	}
}

// runInspect prints the CA-signed certificates currently held. Unlike the
// rest of the ssh commands this writes to stdout: its output is the point of
// running it, not commentary alongside some other effect.
func runInspect(root *RootCommand, out io.Writer) error {
	writeStorage(out, root.Agent())

	keys, err := root.Agent().List(true)
	if err != nil {
		return fmt.Errorf("list identities in %s: %w", root.Agent().Backend(), err)
	}
	if len(keys) == 0 {
		fmt.Fprintf(out, "\nNo certificates signed by your CA are loaded in %s.\n", root.Agent().Backend())
		return nil
	}

	for _, key := range keys {
		fmt.Fprintln(out)
		cert, ok := (*key).(*ssh.Certificate)
		if !ok {
			// List(true) filters to certificates, so this is unreachable
			// short of a backend bug — reported rather than skipped silently.
			fmt.Fprintf(out, "%s (not a certificate)\n", (*key).Type())
			continue
		}
		writeCertificate(out, cert)
	}
	return nil
}

// keyFileLister is the part of a file-backed agent this report needs: the
// paths it keeps the identity in. Declared here rather than added to
// agent.Agent because only the file backend has an answer -- an ssh-agent
// holds keys in a process, not in files -- so the report asks for it and
// prints nothing extra when the backend cannot say.
type keyFileLister interface {
	KeyFiles() []string
}

// writeStorage names where the certificates below are being kept. "In your
// agent" and "in these files" send a reader looking in completely different
// places when something is wrong, and for the file backend the paths are the
// answer to "which key is this, then" -- a question the certificate fields
// alone cannot settle when ~/.ssh holds several.
func writeStorage(out io.Writer, ag agent.Agent) {
	fmt.Fprintf(out, "%-16s %s\n", "Storage", storageDescription(ag))

	files, ok := ag.(keyFileLister)
	if !ok {
		return
	}
	paths := files.KeyFiles()
	if len(paths) == 0 {
		fmt.Fprintf(out, "%-16s %s\n", "Key files", "(none on disk)")
		return
	}
	// Label only the first line, so several paths read as one list rather
	// than as several unrelated fields.
	label := "Key files"
	for _, path := range paths {
		fmt.Fprintf(out, "%-16s %s\n", label, path)
		label = ""
	}
}

// writeCertificate renders one certificate as aligned label/value lines,
// deliberately close to `ssh-keygen -L` so the two can be read side by side.
func writeCertificate(out io.Writer, cert *ssh.Certificate) {
	fmt.Fprintf(out, "%-16s %s\n", "Principals", principalList(cert))
	fmt.Fprintf(out, "%-16s %s\n", "Key ID", orNone(cert.KeyId))
	fmt.Fprintf(out, "%-16s %s\n", "Type", certTypeName(cert.CertType))
	fmt.Fprintf(out, "%-16s %s\n", "Expires", expiryPhrase(cert))
	fmt.Fprintf(out, "%-16s %s\n", "Serial", fmt.Sprint(cert.Serial))
	fmt.Fprintf(out, "%-16s %s\n", "Extensions", orNone(sortedKeys(cert.Extensions)))
	fmt.Fprintf(out, "%-16s %s\n", "Critical options", orNone(criticalOptionList(cert.CriticalOptions)))
}

// certTypeName renders the SSH certificate type constant as the word a human
// uses for it.
func certTypeName(certType uint32) string {
	switch certType {
	case ssh.UserCert:
		return "user"
	case ssh.HostCert:
		return "host"
	default:
		return fmt.Sprintf("unknown (%d)", certType)
	}
}

// sortedKeys renders a certificate's extension map — whose values are empty
// for every extension ssoossh grants — as a stable comma-separated list. Map
// iteration order would otherwise change between runs of the same command.
func sortedKeys(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// criticalOptionList renders critical options with their values, since unlike
// extensions the value is the whole point (the command being forced, the
// addresses being allowed).
func criticalOptionList(options map[string]string) string {
	if len(options) == 0 {
		return ""
	}
	names := make([]string, 0, len(options))
	for name := range options {
		names = append(names, name)
	}
	sort.Strings(names)

	rendered := make([]string, 0, len(names))
	for _, name := range names {
		if value := options[name]; value != "" {
			rendered = append(rendered, name+"="+value)
			continue
		}
		rendered = append(rendered, name)
	}
	return strings.Join(rendered, ", ")
}

// orNone substitutes a placeholder for an empty value, so a field is never
// rendered as a dangling label.
func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}
