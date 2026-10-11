package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/version"
)

// execCommand hands the connection to the relay: handOff, which replaces
// this process where the platform can and runs the relay as a child where
// it cannot (Windows). A variable so tests can observe what argv is handed
// over — calling the real thing would replace the test binary itself, which
// is also why the argv[0] bug below went unnoticed.
var execCommand = handOff

// errProxyCommandRequiresArgs is returned when "ssh proxycommand" is
// invoked with nothing to exec after it.
var errProxyCommandRequiresArgs = errors.New("proxycommand requires a command to exec after it, e.g. \"" +
	version.Name + " ssh proxycommand /usr/bin/nc ...\"")

// errProxyCommandNeedsAgent is returned when keys would go to files: no
// ssh-agent is reachable, or use_agent is off although one is. ssh reads key
// files once, at startup, before ProxyCommand runs, so a certificate written
// to disk here would never be used.
var errProxyCommandNeedsAgent = errors.New("ssh proxycommand needs its key in an ssh-agent, " +
	"because ssh reads key files only once at startup; run an agent (SSH_AUTH_SOCK) with use_agent " +
	"left on, or use the Match exec recipe from '" + version.Name + " ssh config', which works with key files")

// clientOnlyFlags are ssoossh flags no relay is likely to take. Seen after
// the relay command, they were almost certainly meant for ssoossh. -c and
// -v are left out on purpose: they are ordinary relay flags (nc -v, ssh -c),
// and warning on them would print noise in front of every connection.
var clientOnlyFlags = []string{"--server", "--config", "--debug"}

// warnMisplacedClientFlags warns about each ssoossh flag that appears after
// the relay command. Everything from args[0] on is handed to the relay
// unparsed, so such a flag silently goes to the relay while ssoossh runs on
// whatever the configuration says, which may be a different server.
func warnMisplacedClientFlags(w io.Writer, args []string) {
	for _, arg := range args[1:] {
		for _, flag := range clientOnlyFlags {
			if arg == flag || strings.HasPrefix(arg, flag+"=") {
				fmt.Fprintf(w, "Warning: %s comes after the relay command, so %s receives it, not %s; "+
					"put %s flags before %q\n", flag, args[0], version.Name, version.Name, args[0])
			}
		}
	}
}

func newSSHProxyCommandCommand() simplecobra.Commander {
	// The binary's name is written out rather than taken from os.Args[0]:
	// the docs generators build this text too, and the shipped pages said
	// "genclidocs ssh ProxyCommand".
	relay := "/usr/bin/nc -X connect -x 192.0.2.0:8080 %h %p"
	sc := &simpleCommand{
		name:    "proxycommand",
		argSpec: "<command> [args...]",
		short:   "Ensure a valid certificate, then hand the connection to a relay command such as nc.",
		long: "For use as ssh_config's ProxyCommand. Ensures a valid certificate is in your " +
			"ssh-agent, then hands the connection to the relay command that follows it. Write " +
			"the relay command exactly as you would without " + version.Name + ":\n\n" +
			"    Before: ProxyCommand " + relay + "\n" +
			"    After:  ProxyCommand " + version.Name + " ssh proxycommand " + relay + "\n\n" +
			"Everything from the relay command on is passed to it unparsed, so put " +
			version.Name + " flags such as --server, --config or -v before it.\n\n" +
			"Requires an ssh-agent: ssh reads key files once, at startup, so a certificate " +
			"written to disk afterwards would never be used. Without an agent, use the Match " +
			"exec recipe from `" + version.Name + " ssh config`.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			if len(args) == 0 {
				return errProxyCommandRequiresArgs
			}
			if root.ssh.Type() == agent.AgentTypeFile {
				return errProxyCommandNeedsAgent
			}
			warnMisplacedClientFlags(cd.CobraCommand.ErrOrStderr(), args)

			// Ensure there is a usable certificate before handing the process
			// off — that is the entire reason this wraps the relay rather
			// than being configured as a bare ProxyCommand. A valid one is
			// reused, so the common case adds no browser round trip and no
			// perceptible delay.
			//
			// Progress goes to stderr: stdout is the SSH stream from here on,
			// and anything written to it corrupts the connection.
			if err := runLogin(ctx, root, cd.CobraCommand.ErrOrStderr(), false); err != nil {
				return err
			}

			// We hand the connection to the relay and step out of the way:
			// replacing this process, or on Windows waiting on it as its
			// parent. User configuration of ProxyCommand should use nc like
			// the ssh_config example shows.
			// TODO: Decide if we should try to find nc first? If we don't then proxy the
			//  connection ourselves instead of getting out of the way?
			// args is cobra's already-parsed positional args (everything after
			// "ssh proxycommand"), not raw os.Args — using os.Args here would
			// break whenever a global flag precedes the subcommand.
			//
			// argv includes the program name — args, not args[1:]. Passing
			// the tail shifts every argument down one, so the command reads
			// its first real argument as its own name and silently receives
			// one fewer than it was given.
			//
			// The hand-off returns only on failure, or on Windows once the
			// relay has exited. Naming the command in the error matters more
			// here than elsewhere: ssh reports this as a bare "Connection
			// closed", so a naked "no such file or directory" leaves nothing
			// at all to go on.
			// Executing caller-supplied arguments is the entire purpose here:
			// this is ssh's own ProxyCommand line, and the user configured it.
			err := execCommand(args[0], args, os.Environ())
			var exitErr *exec.ExitError
			switch {
			case errors.As(err, &exitErr):
				// The relay ran and failed, and has already said why on
				// stderr. Only a child relay can get here.
				return fmt.Errorf("the proxy command %q exited with status %d", args[0], exitErr.ExitCode())
			case err != nil:
				return fmt.Errorf("failed to run the proxy command %q: %w", args[0], err)
			}
			return nil
		},
		// Everything after "proxycommand" is the command to exec, so cobra
		// must not try to interpret any of it as flags of ours.
		init: func(cd *simplecobra.Commandeer) error {
			// Its own check in run gives a missing relay command a better
			// message (with an example) than checkArity's.
			cd.CobraCommand.Args = cobra.ArbitraryArgs
			cd.CobraCommand.Flags().SetInterspersed(false)
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
