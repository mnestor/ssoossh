// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"github.com/mnestor/ssoossh/internal/ssh"
	ssha "github.com/mnestor/ssoossh/internal/ssh"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	loginCmd := &cobra.Command{
		Use:     "login",
		Short:   "generate ssh keypair and retireve certificate",
		RunE:    loginRun,
		PreRunE: preRun,
	}

	loginCmd.Flags().Int("key-size", 4096, "Key Size to generate (2048, 4096)")
	loginCmd.Flags().Bool("type-rsa", false, "Generate RSA SSH keypair (default)")
	loginCmd.Flags().Bool("type-ec", false, "Generate EC SSH keypair")
	loginCmd.MarkFlagsMutuallyExclusive("type-rsa", "type-ec")
	return loginCmd
}

func loginRun(cmd *cobra.Command, args []string) error {
	agent := cmd.Context().Value(AGENT_CTX).(*ssha.Agent)
	if agent.HasKeys() {
		return nil
	}
	config := cmd.Context().Value(CONFIG_CTX).(Config)

	kp, err := ssh.NewKeyPair(config.KeyTypeRSA, config.KeyTypeEC, config.KeySize, "user")
	if err != nil {
		return err
	}

	return getCertIntoAgent(cmd.Context(), kp, false)
}
