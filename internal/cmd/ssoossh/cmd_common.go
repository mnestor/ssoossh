// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"fmt"
	"os"
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
	if config.Server == "" {
		fmt.Fprintf(errWriter, "must set server being used!\n")
		os.Exit(1)
	}

	var err error
	apiClient = api.GetClient(outWriter, errWriter, config.Server)

	ca, err = apiClient.GetCA()
	if err != nil {
		return err
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
