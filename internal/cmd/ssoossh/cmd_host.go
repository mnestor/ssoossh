// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

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

	config := cmd.Context().Value(CONFIG_CTX).(Config)
	p, err := os.ReadFile(config.HostPubkey) // the file is inside the local directory
	if err != nil {
		return err
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(p))
	if err != nil {
		return err
	}

	kp := &issh.KeyPair{
		Public: &pubKey,
		Type:   "host",
	}

	if err := getCert(cmd.Context(), kp, false); err != nil {
		return err
	}

	if config.WriteCert {
		if !strings.HasSuffix(config.HostPubkey, ".pub") {
			fmt.Fprintf(outWriter, "Unable to figure out filename to create, missing .pub extension\n")
		} else {
			fs := strings.Split(config.HostPubkey, ".")
			fs[0] = fmt.Sprintf("%s-cert.pub", fs[0])
			newFile := strings.Join(fs, ".")
			if err := os.WriteFile(newFile, []byte(kp.CertString), 0600); err != nil {
				return err
			}
			fmt.Fprintf(outWriter, "Certificate saved to: %s\n", fs)
			return nil
		}
	}

	fmt.Fprintf(errWriter, "%s", kp.CertString)

	return nil
}
