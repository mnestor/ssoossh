// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"fmt"
	"os/exec"
	"runtime"

	api "github.com/mnestor/ssoossh/internal/api/client"
	sc "github.com/mnestor/ssoossh/internal/cmd/ssoossh/ssoossh_context"
	config "github.com/mnestor/ssoossh/internal/config/client"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Login to the SSO SSH service",
		Long:  "Login to the SSO SSH service to retrieve and install your SSH certificate to your ssh-agent or to files.",
		RunE:  loginRun,
	}

	return loginCmd
}

func loginRun(cmd *cobra.Command, args []string) error {
	ag := cmd.Context().Value(sc.ContextKeyAgent).(agent.Agent)
	cfg := cmd.Context().Value(sc.ContextKeyConfig).(config.Config)
	apiClient := cmd.Context().Value(sc.ContextKeyAPIClient).(api.ClientI)

	ag.CleanupAgent()

	if c, e := ag.Certificates(); e == nil && c != nil && len(c) > 0 {
		return nil
	}

	kp, err := keypair.NewSshKeypair(cfg.KeyType, cfg.KeySize)
	if err != nil {
		return err
	}

	pubkey, err := kp.MarshalAuthorizedKey()
	if err != nil {
		return fmt.Errorf("unable to get public key: %w", err)
	}
	id, err := apiClient.PostPubKey(pubkey, "user", "")
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/approve/%s", cfg.Server, id)
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

	if err = kp.ParseCertificateFromString(cert); err != nil {
		return fmt.Errorf("unable to parse certificate: %w", err)
	}

	return ag.AddKeypair(kp)
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
