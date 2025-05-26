// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"

	sc "github.com/mnestor/ssoossh/internal/cmd/ssoossh/ssoossh_context"
	config "github.com/mnestor/ssoossh/internal/config/client"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
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
		Short: "Proxy Command to connect to a remote host after loading your SSH certificate",
		Long: `Proxy Command to connect to a remote host after loading your SSH certificate into your ssh-agent. 
Note: use of ssh-agent is required since ssh will read the file before we write it.
This command allows you to connect to a remote host using the SSH certificate loaded into your ssh-agent.`,
		Example: `ssoossh ssh proxycommand <host> [<port>]`,
		Args: cobra.MatchAll(
			cobra.MinimumNArgs(1),
			cobra.MaximumNArgs(2),
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
	if len(args) == 1 {
		host = args[0]
		port = 22 // default SSH port
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
	ag := cmd.Context().Value(sc.ContextKeyAgent).(agent.Agent)

	certs, _ := ag.Certificates()
	if len(certs) == 0 {
		if err := loginRun(cmd, args); err != nil {
			return err
		}
	}

	cfg := cmd.Context().Value(sc.ContextKeyConfig).(config.Config)
	if !cfg.UseAgent {
		return errors.New("ssh-agent must be used since ssh will read the file before we write it")
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
