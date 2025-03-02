// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"io"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/mnestor/ssoossh/internal/ssh"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var (
	host string
	port int
)

var proxyCmd = &cobra.Command{
	Use:   "proxycommand",
	Short: "retrieve certificates and proxy through an ssh bastion host",
	Args: cobra.MatchAll(
		cobra.MinimumNArgs(1),
		cobra.MaximumNArgs(2),
		cobra.OnlyValidArgs,
	),
	RunE:    proxyCommand,
	PreRunE: preGetCertRun,
}

func proxyCommand(cmd *cobra.Command, args []string) error {
	if !agent.HasKeys() {
		kp, err := ssh.NewKeyPair(config.KeyTypeRSA, config.KeyTypeEC, config.KeySize)
		if err != nil {
			return err
		}

		if err := getCertIntoAgent(kp); err != nil {
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
		io.Copy(conn, os.Stdin)
		conn.CloseWrite()
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		io.Copy(os.Stdout, conn)
		conn.CloseRead()
		wg.Done()
	}()

	wg.Wait()
	return nil
}
