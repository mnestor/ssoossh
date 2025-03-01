// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"fmt"
	"io"
	"strconv"

	api "github.com/mnestor/ssoossh/internal/api/client"
	"github.com/mnestor/ssoossh/internal/ssh"
	"github.com/spf13/cobra"
)

var (
	outWriter  io.Writer
	errWriter  io.Writer
	server     string
	agent      *ssh.Agent
	apiClient  *api.Client
	ca         string
	keyTypeEC  bool
	keyTypeRSA bool
	keySize    int
)

var sshCmd = &cobra.Command{
	Use:   "ssh",
	Short: "retrieve certificates and proxy through an ssh bastion host",
}

func init() {
	// set default port
	port = 22
}

func GetCommand(o io.Writer, e io.Writer) *cobra.Command {
	outWriter = o
	errWriter = e

	sshCmd.PersistentFlags().StringVarP(&server, "server", "s", "", "server that signs pubkeys")
	_ = sshCmd.MarkFlagRequired("server")

	sshCmd.AddCommand(listCmd)
	sshCmd.AddCommand(inspectCmd)
	sshCmd.AddCommand(logoutCmd)

	// these 2 need to add some extra parameters
	sshCmd.AddCommand(proxyCmd)
	proxyCmd.Flags().IntVar(&keySize, "size", 4096, "Key Size to generate (2048, 4096)")
	proxyCmd.Flags().BoolVar(&keyTypeRSA, "type-rsa", false, "Generate RSA SSH keypair (default)")
	proxyCmd.Flags().BoolVar(&keyTypeEC, "type-ec", false, "Generate EC SSH keypair")
	proxyCmd.MarkFlagsMutuallyExclusive("type-rsa", "type-ec")

	sshCmd.AddCommand(loginCmd)
	loginCmd.Flags().IntVar(&keySize, "size", 4096, "Key Size to generate (2048, 4096)")
	loginCmd.Flags().BoolVar(&keyTypeRSA, "type-rsa", false, "Generate RSA SSH keypair (default)")
	loginCmd.Flags().BoolVar(&keyTypeEC, "type-ec", false, "Generate EC SSH keypair")
	loginCmd.MarkFlagsMutuallyExclusive("type-rsa", "type-ec")

	return sshCmd
}

func preRun(cmd *cobra.Command, args []string) error {
	var err error
	apiClient = api.GetClient(outWriter, errWriter, server)

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
