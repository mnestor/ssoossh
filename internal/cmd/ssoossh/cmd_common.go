// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"

	api "github.com/mnestor/ssoossh/internal/api/client"
	"github.com/mnestor/ssoossh/internal/ssh"
	ssha "github.com/mnestor/ssoossh/internal/ssh"
	"github.com/spf13/cobra"
)

var (
	agent     *ssha.Agent
	apiClient *api.Client
	ca        string
)

func init() {
	// set default port
	port = 22
}

func preRun(cmd *cobra.Command, args []string) error {
	var err error
	apiClient = api.GetClient(config.Server)

	ca, err = apiClient.GetCA()
	if err != nil {
		return errors.New("unable to talk to server please check your configuration")
	}

	agent, err = ssh.GetAgent()
	if err != nil {
		return err
	}

	agent.LoadCA(ca)

	return nil
}
func preGetCertRun(cmd *cobra.Command, args []string) error {
	e := preRun(cmd, args)
	if e != nil {
		return e
	}

	if len(args) == 1 {
		host = args[0]
	} else {
		host = args[0]
		var err error
		port, err = strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("unable to convert %s to port number (default: 22)", args[1])
		}
	}

	return nil
}

func getCert(kp *ssh.KeyPair, allToErr bool) error {
	var out = errWriter
	if !allToErr {
		out = outWriter
	}

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

	fmt.Fprintf(out, "We tried to open your brower to the following URL:\n\n%s\n\nIf that failed to open your browser please visit the URL to continue!\n", url)

	// wait for cert
	var cert string
	cert, err = apiClient.GetCertificate(id)
	if err != nil {
		return err
	}

	if cert == "" {
		return errors.New("empty response")
	}

	if err = kp.ParseCertificate(cert); err != nil {
		return err
	}
	return nil
}

func getCertIntoAgent(kp *ssh.KeyPair, allToErr bool) error {
	if err := getCert(kp, allToErr); err != nil {
		return err
	}

	return agent.AddCertificate(kp)
}
