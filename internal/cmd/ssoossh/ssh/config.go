package ssh

import (
	sc "github.com/mnestor/ssoossh/internal/cmd/ssoossh/ssoossh_context"
	config "github.com/mnestor/ssoossh/internal/config/client"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"

	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print settings for ssh_config based on the current configuration",
		RunE:  configRun,
	}

	return cmd
}

func configRun(cmd *cobra.Command, args []string) error {
	ag := cmd.Context().Value(sc.ContextKeyAgent).(agent.Agent)
	cfg := cmd.Context().Value(sc.ContextKeyConfig).(config.Config)

	switch ag.Type() {
	case agent.AgentTypeSsh:
		cmd.Println("# Match all hosts and use the agent")
		cmd.Print("Host *\n")
		cmd.Printf("  # Just making sure we use the agent\n")
		cmd.Printf("  IdentityAgent SSH_AUTH_SOCK\n")

	case agent.AgentTypeFile:
		cmd.Println("# Match all hosts and use files")
		cmd.Println("# Must use match exec when using files since ssh will pick up the file before we write it using proxy command")
		cmd.Printf("Match exec \"ssoossh ssh login\"\n")
		cmd.Printf("  # Use only the identity we specify\n")
		cmd.Printf("  IdentitiesOnly yes\n")
		cmd.Printf("  IdentitiyAgent none\n")
		cmd.Printf("  IdentityFile %s\n", cfg.SshKeyIdentityFile)
		cmd.Printf("  CertificateFile %s-cert.pub\n", cfg.SshKeyIdentityFile)
	}

	return nil
}
