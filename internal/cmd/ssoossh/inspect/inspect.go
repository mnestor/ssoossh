// Created By Mike Nestor <me@mikenestor.org>
package inspect

import (
	"fmt"
	"time"

	sc "github.com/mnestor/ssoossh/internal/cmd/ssoossh/ssoossh_context"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"golang.org/x/crypto/ssh"

	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "inspect key that exists in ssh-agent or file",
		Long:  "Inspect the SSH certificate loaded in the ssh-agent or file.",
		RunE:  inspectRun,
	}

	cmd.PersistentFlags().BoolP("use-agent", "a", true, "Use SSH agent for key management.")
	cmd.PersistentFlags().BoolP("fallback-file-agent", "f", true, "Use file-based key management if SSH agent is not available.")

	return cmd
}

func inspectRun(cmd *cobra.Command, args []string) error {
	ag := cmd.Context().Value(sc.ContextKeyAgent).(agent.Agent)
	agentCerts, err := ag.Certificates()
	if err != nil {
		return err
	}

	if len(agentCerts) == 0 {
		cmd.Println("no certificates from our ca present in ssh-agent")
	}

	for _, agentKey := range agentCerts {
		var certType string

		switch agentKey.CertType {
		case ssh.HostCert:
			certType = "host"
		case ssh.UserCert:
			certType = "user"
		default:
			certType = "unknown"
		}

		space := ""
		fmt.Println("")
		fmt.Println("Valid certificates that are signed by SSH Certificate Service")
		fmt.Println("")
		fmt.Printf("%8sType: %s %s certificate\n", space, agentKey.Type(), certType)
		fmt.Printf("%8sPublic key: %s-CERT %s\n", space, keyType(agentKey.Key), ssh.FingerprintSHA256(agentKey.Key))
		fmt.Printf("%8sSigning CA: %s %s\n", space, keyType(agentKey.SignatureKey), ssh.FingerprintSHA256(agentKey.SignatureKey))
		fmt.Printf("%8sKey ID: \"%s\"\n", space, agentKey.KeyId)
		fmt.Printf("%8sSerial: %d\n", space, agentKey.Serial)
		fmt.Printf("%8sValid: from %s to %s\n", space,
			time.Unix(int64(agentKey.ValidAfter), 0).Local().Format("2006-01-02T15:04:05"),
			time.Unix(int64(agentKey.ValidBefore), 0).Local().Format("2006-01-02T15:04:05"))
		fmt.Printf("%8sPrincipals: ", space)
		if len(agentKey.ValidPrincipals) == 0 {
			fmt.Println("(none)")
		} else {
			fmt.Println()
			for _, p := range agentKey.ValidPrincipals {
				fmt.Printf("%16s%s\n", space, p)
			}
		}
		fmt.Printf("%8sCritical Options: ", space)
		if len(agentKey.CriticalOptions) == 0 {
			fmt.Println("(none)")
		} else {
			fmt.Println()
			for k, v := range agentKey.CriticalOptions {
				fmt.Printf("%16s%s %v\n", space, k, v)
			}
		}
		fmt.Printf("%8sExtensions: ", space)
		if len(agentKey.Extensions) == 0 {
			fmt.Println("(none)")
		} else {
			fmt.Println()
			for k, v := range agentKey.Extensions {
				fmt.Printf("%16s%s %v\n", space, k, v)
			}
		}
	}

	return nil
}

func keyType(key ssh.PublicKey) string {
	switch key.Type() {
	case ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoSKECDSA256:
		return "ECDSA"
	case ssh.KeyAlgoED25519, ssh.KeyAlgoSKED25519:
		return "ED25519"
	case ssh.KeyAlgoRSA:
		return "RSA"
	case ssh.KeyAlgoDSA:
		return "DSA"
	default:
		return "unknown"
	}
}
