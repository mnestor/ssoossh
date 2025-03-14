// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"encoding/pem"
	"fmt"
	"os"

	issh "github.com/mnestor/ssoossh/internal/ssh"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

func newServiceCmd() *cobra.Command {
	serviceCmd := &cobra.Command{
		Use:     "service",
		Short:   "submit host keys and get the certificate",
		Long:    "If you disable writing the new file the certificate is written to Stderr instead of Stdout for piping.",
		RunE:    serviceRun,
		PreRunE: preRun,
	}

	serviceCmd.Flags().StringP("write-file", "w", "id_ssoossh-username", "Filename for private key. Other files will be based on that")
	serviceCmd.Flags().StringP("username", "u", "(none)", "service account name to embed")
	_ = serviceCmd.MarkFlagRequired("username")
	return serviceCmd
}

func serviceRun(cmd *cobra.Command, args []string) error {
	config := cmd.Context().Value(CONFIG_CTX).(Config)
	kp, err := issh.NewKeyPair(config.KeyTypeRSA, config.KeyTypeEC, config.KeySize, "service")
	if err != nil {
		return err
	}

	kp.Username = config.Username

	if err := getCert(cmd.Context(), kp, false); err != nil {
		return err
	}

	pKeyRaw, err := ssh.MarshalPrivateKey(kp.Private, fmt.Sprintf("ssoossh-%s", config.Username))
	if err != nil {
		fmt.Fprintln(outWriter, "Unable to convert private key to usable format")
		return nil
	}

	outputFile := config.WriteFile
	if outputFile == "id_ssoossh-username" {
		outputFile = fmt.Sprintf("id_ssoossh-%s", config.Username)
	}

	pubFile := fmt.Sprintf("%s.pub", outputFile)
	certFile := fmt.Sprintf("%s-cert.pub", outputFile)

	if err := os.WriteFile(pubFile, []byte(kp.String()), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(certFile, []byte(kp.CertString), 0600); err != nil {
		return err
	}

	privatePem, err := os.Create(outputFile)
	if err != nil {
		return err
	}
	if err := pem.Encode(privatePem, pKeyRaw); err != nil {
		return err
	}
	privatePem.Close()
	if err := os.Chmod(outputFile, 0600); err != nil {
		return err
	}

	fmt.Fprintf(outWriter, "ssh keys saved to: %s{,.pub,-cert.pub}\n", outputFile)

	return nil
}
