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
	config := getConfig(cmd.Context())
	apiClient := getApiClient(cmd.Context())
	kp, err := issh.NewKeyPair(config.KeyTypeRSA, config.KeyTypeEC, config.KeySize, "service", config.Username)
	if err != nil {
		return err
	}

	id, err := apiClient.PostPubKey(kp)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/approve/%s", config.Server, id)
	cmd.Printf("Please visit the URL to continue!:\n%s\n", url)

	var cert string
	if cert, err = apiClient.GetCertificate(id); err != nil {
		return err
	}

	pKeyRaw, err := ssh.MarshalPrivateKey(kp.GetPrivate(), fmt.Sprintf("ssoossh-%s", config.Username))
	if err != nil {
		cmd.Printf("Unable to convert private key to usable format")
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
	if err := os.WriteFile(certFile, []byte(cert), 0600); err != nil {
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

	cmd.Printf("ssh keys saved to: %s{,.pub,-cert.pub}\n", outputFile)

	return nil
}
