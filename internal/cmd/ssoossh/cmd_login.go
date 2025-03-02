// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

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
	if agent.HasKeys() {
		return nil
	}

	kp, err := ssh.NewKeyPair(config.KeyTypeRSA, config.KeyTypeEC, config.KeySize)
	if err != nil {
		return err
	}

	return getCertIntoAgent(kp)
}

func getCertIntoAgent(kp *ssh.KeyPair) error {
	id, err := apiClient.PostPubKey(kp)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/approve/%s", config.Server, id)

	// try to open the users browser
	// ignore the error since we print the url on the screen anyway
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("xdg-open", url).Start()
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		_ = exec.Command("open", url).Start()
	default:
		_ = fmt.Errorf("unsupported platform")
	}

	fmt.Fprintf(errWriter, "We tried to open your brower to the following URL:\n\n%s\n\nIf that failed to open your browser please visit the URL to continue!\n", url)

	// wait for cert
	var cert string
	cert, err = apiClient.GetCertificate(id)
	if err != nil {
		return err
	}

	if err = kp.ParseCertificate(cert); err != nil {
		return err
	}

	if err = agent.AddCertificate(kp); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
	}

	return nil
}
