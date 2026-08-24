package cmd

import (
	"context"
	"io"

	"github.com/bep/simplecobra"
)

func newSSHConfigCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "config",
		short: "Print the ssh_config lines that hook ssoossh into your existing ssh.",
		// Deliberately offline: this is a recipe, not a report. It answers
		// the same way whether or not a server is configured, reachable, or
		// trusted, which is exactly when someone is most likely to be
		// reading it. See offlineCommander in offline.go.
		offline: true,
		long: "Prints the ssh_config recipes that make `ssh` invoke ssoossh, with the " +
			"rules that decide which one you want. Contacts nothing and reads nothing " +
			"but this text, so it answers before a server is configured and keeps " +
			"answering after one stops responding.\n\n" +
			sshConfigGuidance +
			"\nFor what this machine actually resolved — the config files that were " +
			"merged and what came of each, the settings that resulted, where the key " +
			"files are and whether they exist — add --debug to the command you are " +
			"actually running. That is the one place those are reported, so there is " +
			"no second version of the truth to keep in step with this one.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return runConfig(cd.CobraCommand.OutOrStdout())
		},
	}
}

// runConfig prints the wiring guidance. It takes no root state on purpose:
// nothing here varies with configuration, which is what lets the command
// work when configuration is the broken thing.
func runConfig(out io.Writer) error {
	_, err := io.WriteString(out, sshConfigGuidance)
	return err
}

// sshConfigGuidance is how ssoossh gets hooked into ssh_config, kept here
// because `ssh config` is where someone looks for it. It is the command's
// entire output and is embedded in its long help, so a plain run, --help
// and the man page cannot come to say different things. Mirrors
// docs/configuration.md, which is the long form with the sshd and PAM ends
// of the setup that do not belong in a client help page.
const sshConfigGuidance = `The client is never run on its own — ssh invokes it. Two ways to arrange that:

  Match exec (recommended). Runs before ssh connects, so a certificate that
  is already valid is reused and nothing opens a browser until it expires. A
  non-zero exit blocks the connection. Works with or without an ssh-agent.

      Match host bastion.example.com exec "ssoossh ssh login"
          User youruser

  ProxyCommand. Ensures a valid certificate, then hands off to whatever relay
  command follows it — useful when reaching the target also needs an HTTP or
  SOCKS proxy. Requires an ssh-agent: ssh reads IdentityFile and
  CertificateFile once at startup, so a certificate refreshed on disk after
  that point goes unused.

      Host jump.example.com
          ProxyCommand ssoossh ssh proxycommand /usr/bin/nc -X connect -x 192.0.2.0:8080 %h %p

Service accounts are wired up differently, from an enrollment code rather
than a browser login; ` + "`ssoossh service enroll`" + ` prints that recipe, with the
real paths filled in, at the end of a successful enrollment.
`
