package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/server/bootstrap"
)

var _ simplecobra.Commander = (*signCommand)(nil)

// signCommand runs the signer-only mode: just the signing component with no
// database, no HTTP server, no OIDC/LDAP. Requires NATS for communication
// with the API instances.
type signCommand struct{}

func newSignCommand() simplecobra.Commander { return &signCommand{} }

// Name implements simplecobra.Commander.
func (c *signCommand) Name() string { return "sign" }

// Commands implements simplecobra.Commander.
func (c *signCommand) Commands() []simplecobra.Commander { return nil }

// Init implements simplecobra.Commander.
func (c *signCommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Run the signer process only."
	cd.CobraCommand.Long = "Signer mode runs only the certificate signing component: " +
		"it consumes signing requests from the message broker (NATS) and publishes signed certificates. " +
		"This mode requires:\n" +
		"  - NATS broker configured (gochannel is not supported for multi-process)\n" +
		"  - CA private key location (via ssh_key config)\n" +
		"  - mTLS credentials for NATS (cert_file, key_file, ca_file)\n\n" +
		"No database, HTTP server, or OIDC/LDAP configuration is needed. " +
		"Use this to isolate the signer from the webserver's attack surface."
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *signCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	return nil
}

// Run implements simplecobra.Commander.
func (c *signCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return bootstrap.BootstrapSigner(cd.CobraCommand)
}
