// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/mnestor/ssoossh/internal/ssh"
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
	config := getConfig(cmd.Context())
	agent := getAgent(cmd.Context())
	apiClient := getApiClient(cmd.Context())
	if agent.HasKeys() {
		return nil
	}

	kp, err := ssh.NewKeyPair(config.KeyTypeRSA, config.KeyTypeEC, config.KeySize, "user", "")
	if err != nil {
		return err
	}

	id, err := apiClient.PostPubKey(kp)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/approve/%s", config.Server, id)
	tryOpenBrowser(url)

	cmd.PrintErrf(`We tried to open your brower to the following URL:

%s

If that failed to open your browser please visit the URL to continue!
`, url)

	var cert string
	if cert, err = apiClient.GetCertificate(id); err != nil {
		return err
	}

	if cert == "" {
		return errors.New("empty response")
	}

	if err = kp.ParseCertificate(cert); err != nil {
		return err
	}

	return agent.AddCertificate(kp)
}

func tryOpenBrowser(u string) {
	// try to open the users browser
	// ignore the error since we print the url on the screen anyway
	switch runtime.GOOS {
	case "linux":
		_ = exec.Command("xdg-open", u).Start()
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		_ = exec.Command("open", u).Start()
	default:
		_ = fmt.Errorf("unsupported platform")
	}
}
