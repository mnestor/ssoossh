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

var serviceCmd = &cobra.Command{
	Use:     "service",
	Short:   "submit host keys and get the certificate",
	Long:    "If you disable writing the new file the certificate is written to Stderr instead of Stdout for piping.",
	RunE:    serviceRun,
	PreRunE: preRun,
}

var (
	outputFile  string
	accountName string
)

func init() {
	serviceCmd.Flags().StringVarP(&outputFile, "write-file", "w", "id_ssoossh-username", "Filename for private key. Other files will be based on that")
	serviceCmd.Flags().StringVarP(&accountName, "username", "u", "(none)", "service account name to embed")
	_ = serviceCmd.MarkFlagRequired("username")
}

func serviceRun(cmd *cobra.Command, args []string) error {
	kp, err := issh.NewKeyPair(config.KeyTypeRSA, config.KeyTypeEC, config.KeySize, "service")
	if err != nil {
		return err
	}

	kp.Username = accountName

	if err := getCert(kp, false); err != nil {
		return err
	}

	pKeyRaw, err := ssh.MarshalPrivateKey(kp.Private, fmt.Sprintf("ssoossh-%s", accountName))
	if err != nil {
		fmt.Fprintln(outWriter, "Unable to convert private key to usable format")
		return nil
	}

	if outputFile == "id_ssoossh-username" {
		outputFile = fmt.Sprintf("id_ssoossh-%s", accountName)
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
