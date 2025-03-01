// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"fmt"
	"os"

	"github.com/mnestor/ssoossh/internal/ssh"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:     "login",
	Short:   "generate ssh keypair and retireve certificate",
	RunE:    loginRun,
	PreRunE: preRun,
}

func loginRun(cmd *cobra.Command, args []string) error {
	return doLogin()
}

func doLogin() error {
	kp, err := ssh.NewKeyPair(keyTypeRSA, keyTypeEC, keySize)
	if err != nil {
		return err
	}

	id, err := apiClient.PostPubKey(kp)
	if err != nil {
		return err
	}

	fmt.Fprintf(errWriter, "\t Please open in your browser: https://ssh.nanestor.us/approve/%s", id)

	// wait for cert
	cert, err := apiClient.GetCertificate(id)
	if err != nil {
		return err
	}

	fmt.Fprintf(errWriter, "cert: %s", cert)

	kp.ParseCertificate(cert)
	agent, err := ssh.GetAgent()
	if err != nil {
		return err
	}

	err = agent.AddCertificate(kp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
	}
	return nil
}
