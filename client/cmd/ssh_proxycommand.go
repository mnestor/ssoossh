package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/version"
)

// errProxyCommandRequiresArgs is returned when "ssh proxycommand" is
// invoked with nothing to exec after it.
var errProxyCommandRequiresArgs = errors.New("proxycommand requires a command to exec after it, e.g. \"" +
	version.Name + " ssh proxycommand /usr/bin/nc ...\"")

func newSSHProxyCommandCommand() simplecobra.Commander {
	exe := filepath.Base(os.Args[0])
	pc := "ProxyCommand /usr/bin/nc -X connect -x 192.0.2.0:8080 %h %p"
	sc := &simpleCommand{
		name:  "proxycommand",
		short: "Ensure a valid certificate, then relay stdio to the target host over TCP.",
		long: "For use as ssh_config's ProxyCommand. Arguments after ProxyCommand should " +
			"mirror exactly as if you weren't calling ssoossh.\n\n" +
			"from ssh_config man page\nBefore: " + pc + "\n" +
			"After : ProxyCommand " + exe + " ssh " + pc + "\n\n" +
			"NOTICE: Use of ssh key " +
			"files will not work here as ssh only reads them once at start and will not see " +
			"our changes to them.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			if root.ssh.Type() == agent.AgentTypeFile {
				return errors.New("file based ssh keys are not supported for proxycommand")
			}
			if len(args) == 0 {
				return errProxyCommandRequiresArgs
			}

			//check login

			// We are handing off our process and stepping out of the way. User configuration
			// of ProxyCommand should use nc like the ssh_config example shows.
			// TODO: Decide if we should try to find nc first? If we don't then proxy the
			//  connection ourselves instead of getting out of the way?
			// args is cobra's already-parsed positional args (everything after
			// "ssh proxycommand"), not raw os.Args — using os.Args here would
			// break whenever a global flag precedes the subcommand.
			//nolint:gosec // we are very deliberatly handing off what was passed in
			return syscall.Exec(args[0], args[1:], os.Environ())
		},
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.Flags().String("something", "value", "usage")
			return nil
		},
	}

	return sc
}

// func proxy(targetAddr string) {
// 	conn, err := net.Dial("tcp", targetAddr)
// 	if err != nil {
// 		panic(err)
// 	}
// 	defer conn.Close()

// 	// Reduce latency artifacts
// 	if tc, ok := conn.(*net.TCPConn); ok {
// 		_ = tc.SetNoDelay(true) //nolint:errcheck
// 	}

// 	// stdin -> conn
// 	errCh := make(chan error, 1)
// 	go func() {
// 		_, err := io.Copy(conn, os.Stdin) // writes conn
// 		// Half-close so remote sees EOF promptly (best effort)
// 		if tc, ok := conn.(*net.TCPConn); ok {
// 			_ = tc.CloseWrite() //nolint:errcheck
// 		}
// 		errCh <- err
// 	}()

// 	// conn -> stdout (blocking)
// 	_, err = io.Copy(os.Stdout, conn)
// 	// wait for stdin side to finish too
// 	<-errCh

// 	_ = err
// }
