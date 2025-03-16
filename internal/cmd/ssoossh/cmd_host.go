// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	issh "github.com/mnestor/ssoossh/internal/ssh"
)

func newHostCmd() *cobra.Command {
	hostCmd := &cobra.Command{
		Use:     "host",
		Short:   "submit host keys and get the certificate",
		Long:    "If you disable writing the new file the certificate is written to Stderr instead of Stdout for piping.",
		RunE:    hostRun,
		PreRunE: preRun,
	}
	hostCmd.Flags().StringP("host-pubkey", "p", "/etc/ssh/ssh_host_rsa_key.pub", "Public key file to submit for signing Env: SSOOSSH_FILE")
	hostCmd.Flags().Bool("write-cert", true, "write certificate to parsed filename (ie. /etc/ssh/ssh_host_rsa_key-cert.pub) Env: SSOOSSH_WRITE_FILE")
	return hostCmd
}

func hostRun(cmd *cobra.Command, args []string) error {
	config := getConfig(cmd.Context())
	apiClient := getApiClient(cmd.Context())

	p, err := os.ReadFile(config.HostPubkey) // the file is inside the local directory
	if err != nil {
		return err
	}

	kp := issh.NewKeyPairForHost(p)

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

	if cert == "" {
		return errors.New("empty response")
	}

	if config.WriteCert {
		if !strings.HasSuffix(config.HostPubkey, ".pub") {
			cmd.Println("Unable to figure out filename to create, missing .pub extension")
		} else {
			fs := strings.Split(config.HostPubkey, ".")
			fs[0] = fmt.Sprintf("%s-cert.pub", fs[0])
			newFile := strings.Join(fs, ".")
			if err := os.WriteFile(newFile, []byte(cert), 0600); err != nil {
				return err
			}
			cmd.Printf("Certificate saved to: %s\n", fs)
			return nil
		}
	}

	cmd.Printf("%s", cert)

	return nil
}
