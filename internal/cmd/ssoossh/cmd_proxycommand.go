// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var (
	host string
	port int
)

func newProxyCmd() *cobra.Command {
	proxyCmd := &cobra.Command{
		Use:   "proxycommand",
		Short: "retrieve certificates and proxy through an ssh bastion host",
		Args: cobra.MatchAll(
			cobra.MinimumNArgs(1),
			cobra.MaximumNArgs(3),
			cobra.OnlyValidArgs,
		),
		RunE:    proxyCommand,
		PreRunE: preGetCertRun,
	}
	proxyCmd.Flags().Int("key-size", 4096, "Key Size to generate (2048, 4096)")
	proxyCmd.Flags().Bool("type-rsa", false, "Generate RSA SSH keypair (default)")
	proxyCmd.Flags().Bool("type-ec", false, "Generate EC SSH keypair")
	proxyCmd.MarkFlagsMutuallyExclusive("type-rsa", "type-ec")
	return proxyCmd
}

func preGetCertRun(cmd *cobra.Command, args []string) error {
	if e := preRun(cmd, args); e != nil {
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

func proxyCommand(cmd *cobra.Command, args []string) error {
	agent := getAgent(cmd.Context())

	if !agent.HasKeys() {
		if err := loginRun(cmd, args); err != nil {
			return err
		}
	}

	return proxyDirect(host, strconv.Itoa(port))
}

func proxyDirect(host, port string) error {
	address := net.JoinHostPort(host, port)
	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return errors.Wrap(err, "error resolving address")
	}

	conn, err := net.DialTCP("tcp", nil, addr)
	if err != nil {
		return errors.Wrapf(err, "error connecting to %s", address)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		_ = conn.CloseWrite()
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		_, _ = io.Copy(os.Stdout, conn)
		_ = conn.CloseRead()
		wg.Done()
	}()

	wg.Wait()
	return nil
}
