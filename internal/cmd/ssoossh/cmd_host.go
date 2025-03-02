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

var hostCmd = &cobra.Command{
	Use:     "host",
	Short:   "submit host keys and get the certificate",
	Long:    "If you disable writing the new file the certificate is written to Stderr instead of Stdout for piping.",
	RunE:    hostRun,
	PreRunE: preRun,
}

var (
	hostPubkey string
	writeOnly  bool
)

func init() {
	hostCmd.Flags().StringVarP(&hostPubkey, "host-pubkey", "p", "/etc/ssh/ssh_host_rsa_key.pub", "Public key file to submit for signing Env: SSOOSSH_FILE")
	hostCmd.Flags().BoolVar(&writeOnly, "write-cert", true, "write certificate to parsed filename (ie. /etc/ssh/ssh_host_rsa_key-cert.pub) Env: SSOOSSH_WRITE_FILE")
}

func hostRun(cmd *cobra.Command, args []string) error {
	p, err := os.ReadFile(hostPubkey) // the file is inside the local directory
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

	if err := getCert(kp, false); err != nil {
		return err
	}

	if writeOnly {
		if !strings.HasSuffix(hostPubkey, ".pub") {
			fmt.Fprintf(outWriter, "Unable to figure out filename to create, missing .pub extension\n")
		} else {
			fs := strings.Split(hostPubkey, ".")
			fs[0] = fmt.Sprintf("%s-cert", fs[0])
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
