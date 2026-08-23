package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"time"

	"github.com/bep/simplecobra"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/api"
	sshagent "github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// loginExtensions are the certificate extensions `ssh login` asks for. The
// server narrows this against its own configuration and the web UI shows
// what survived before anyone approves (see root CLAUDE.md, Hard
// Constraints), so asking for the full interactive set is a request, not a
// demand — anything the deployment does not permit is simply trimmed.
//
// Asking for nothing is not an option: narrowing is an intersection, so an
// empty request yields a certificate carrying no extensions at all, which
// cannot open an interactive session.
var loginExtensions = []string{
	"permit-pty",
	"permit-agent-forwarding",
	"permit-port-forwarding",
	"permit-X11-forwarding",
	"permit-user-rc",
}

// reuseGrace is how much validity a already-loaded certificate must have
// left before `ssh login` will hand it back instead of getting a new one.
// Without it a certificate expiring in the next instant counts as reusable
// and the SSH connection it was reused for fails moments later.
const reuseGrace = 10 * time.Second

// browserLaunchTimeout bounds the best-effort browser launch. The URL is
// always printed first, so a launcher that hangs must not hold up the login.
const browserLaunchTimeout = 5 * time.Second

func newSSHLoginCommand() simplecobra.Commander {
	// --force replaces the loaded certificate: "this one is somehow wrong,
	// get me another". It is deliberately not backed by a config setting.
	// "Never reuse" sounds like a policy an operator would want on a shared
	// machine, but it does not do what it sounds like — see runLogin, which
	// has to remove the superseded certificate for even this flag to mean
	// what it says. Bounding exposure is the server's job, via
	// cert_options.*.valid_duration, which a client cannot opt out of.
	var force bool

	return &simpleCommand{
		name:  "login",
		short: "Authenticate via OIDC and load a signed SSH certificate into the agent (or files).",
		long: "Generates a fresh keypair, sends the public key to the ssoossh server, opens " +
			"the browser for OIDC approval, and waits over SSE for the signed certificate. " +
			"Used from ssh_config's `Match exec`, or interactively before a session.\n\n" +
			"A certificate that is already loaded and still valid is reused rather than " +
			"replaced, so one approval covers every connection until it expires. Pass " +
			"--force to replace it with a new one.",
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.Flags().BoolVar(&force, "force", false,
				"request a new certificate even if a valid one is already loaded")
			// --key-type/--key-size are local to ssh login: it's the only
			// command that generates a keypair. Bound to sshkey.type/size
			// in config.go's newConfig, same mechanism as --server.
			cd.CobraCommand.Flags().String("key-type", "",
				"ssh key algorithm to generate: ed25519, ecdsa, or rsa (default depends on FIPS mode)")
			cd.CobraCommand.Flags().Int("key-size", 0,
				"ssh key size (bits for rsa, curve for ecdsa; ignored for ed25519)")
			return nil
		},
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			// Everything a human reads goes to stderr: this command runs from
			// ssh_config's Match exec, where stdout belongs to ssh and the
			// exit status is the whole contract.
			return runLogin(ctx, root, cd.CobraCommand.ErrOrStderr(), force)
		},
	}
}

// runLogin obtains a usable certificate and loads it into the agent (or key
// files), reusing a valid one when there is one. It returns an error — and
// so a non-zero exit status — for every outcome that does not end with a
// usable certificate, because `Match exec` reads exit status as "may this
// connection proceed".
func runLogin(ctx context.Context, root *RootCommand, out io.Writer, force bool) error {
	cfg := root.Config()

	if !force {
		if cert := reusableCertificate(root.Agent()); cert != nil {
			fmt.Fprintf(out, "Already have a valid certificate for %s, %s.\n",
				principalList(cert), expiryPhrase(cert))
			return nil
		}
	}

	// The resolved algorithm and size are policy (FIPS steering, per-type
	// defaults and limits); generation reads them rather than deciding for
	// itself. Warnings were emitted once at config load.
	algorithm, size, _, err := cfg.ResolveSSHKey()
	if err != nil {
		return fmt.Errorf("invalid ssh key configuration: %w", err)
	}

	kp, err := keypair.NewSSHKeypair(algorithm, size)
	if err != nil {
		return fmt.Errorf("generate %s keypair: %w", algorithm, err)
	}
	publicKey, err := kp.MarshalAuthorizedKey()
	if err != nil {
		return fmt.Errorf("encode public key: %w", err)
	}

	localUsername, localHostname := localIdentity()

	pending, err := root.API().CreateUserRequest(ctx, publicKey, localUsername, localHostname, api.RequestedOptions{
		Extensions:      loginExtensions,
		SourceAddresses: api.LocalInterfaceAddresses(),
	})
	if err != nil {
		return fmt.Errorf("request a certificate: %w", err)
	}

	// Printed before the wait, always, whether or not a browser opens — this
	// is the only way the user learns where to approve.
	fmt.Fprintf(out, "Approve this request in your browser:\n\n    %s\n\n", pending.ApprovalURL)
	if cfg.TryOpenBrowser {
		openBrowser(ctx, out, pending.ApprovalURL)
	}
	fmt.Fprintln(out, "Waiting for approval…")

	result, err := root.API().AwaitCertificate(ctx, pending)
	if err != nil {
		return describeWaitError(err)
	}

	if err := checkOutcome(result); err != nil {
		return err
	}

	if err := kp.ParseCertificateFromString(result.Certificate); err != nil {
		return fmt.Errorf("the issued certificate could not be parsed: %w", err)
	}
	if err := root.Agent().AddKeypair(kp); err != nil {
		return fmt.Errorf("load the certificate into %s: %w", root.Agent().Backend(), err)
	}

	// Adding a certificate to an agent does not replace the one it
	// supersedes — they are different identities, so both stay loaded and
	// both stay valid until they expire on their own. Without this,
	// `--force` would hand out a new certificate while leaving the one the
	// user wanted rid of usable for hours.
	pruneSuperseded(root.Agent(), kp.Certificate(), out)

	fmt.Fprintf(out, "Certificate loaded into %s for %s, %s.\n",
		root.Agent().Backend(), principalList(kp.Certificate()), expiryPhrase(kp.Certificate()))
	return nil
}

// localIdentity returns this machine's local OS username and hostname, for
// a user-type request's LocalUsername/LocalHostname — the local client is
// the requester for this certificate type, so this is who/where the
// request actually came from (see
// docs/dev/changes-next.md). Best-effort: either value is
// left empty on a lookup failure rather than failing the login over
// metadata that isn't a precondition for issuance.
func localIdentity() (username, hostname string) {
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	hostname, _ = os.Hostname() //nolint:errcheck // best-effort audit metadata, not a login precondition — see the doc comment above
	return username, hostname
}

// pruneSuperseded removes the certificates this login replaces: everything
// signed by a trusted CA except the one just issued. Same discriminator as
// `ssh logout` — the user's own keys and other CAs' certificates are not
// ours to touch.
//
// Best-effort and after the fact on purpose. Pruning first would risk
// leaving the user with nothing if issuance then failed, and a certificate
// that could not be removed is not a reason to fail a login that otherwise
// worked — it is worth a word, though, since the stale one still opens
// sessions.
func pruneSuperseded(agent sshagent.Agent, current *ssh.Certificate, out io.Writer) {
	if current == nil {
		return
	}

	loaded, err := agent.List(true)
	if err != nil {
		return
	}

	for _, key := range loaded {
		if key == nil || bytes.Equal((*key).Marshal(), current.Marshal()) {
			continue
		}
		if err := agent.Remove(*key); err != nil {
			fmt.Fprintf(out, "(could not remove the certificate this replaces: %v)\n", err)
		}
	}
}

// checkOutcome turns a resolved request into either nil (a certificate
// arrived) or the error explaining why no certificate will. Every terminal
// status ssoosshd can send is handled: an unhandled one would otherwise be
// reported as success with nothing to load.
func checkOutcome(result *api.CertificateResult) error {
	if result == nil {
		return errors.New("the certificate request resolved with no outcome")
	}

	switch result.Status {
	case api.StatusApproved:
		if result.Certificate == "" {
			return errors.New("the request was approved but no certificate was delivered; run ssh login again")
		}
		return nil
	case api.StatusDenied:
		return errors.New("the request was denied, so no certificate was issued")
	case api.StatusExpired:
		return errors.New("the request expired before anyone approved it; run ssh login again")
	case api.StatusFailed:
		return errors.New("ssoosshd could not issue the certificate; check the server logs, then run ssh login again")
	case api.StatusEnrolled:
		// A user request cannot resolve this way — enrollment belongs to the
		// service path — so seeing it means the server sent something this
		// command has no way to use.
		return errors.New("the server resolved this as a service enrollment, which ssh login cannot use")
	default:
		return fmt.Errorf("the server reported an unrecognized outcome %q", result.Status)
	}
}

// describeWaitError translates a failure while waiting into advice. The 410
// is worth naming: the certificate really was issued, it is simply gone —
// they are never persisted — and running the command again is the fix rather
// than a workaround.
func describeWaitError(err error) error {
	var respErr *api.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == http.StatusGone {
		return errors.New("the certificate was issued but is no longer available for collection; run ssh login again")
	}
	return fmt.Errorf("wait for approval: %w", err)
}

// reusableCertificate returns a loaded certificate that is signed by a
// trusted CA and has enough validity left to be worth reusing, or nil.
//
// Any valid CA-signed certificate counts, without checking that its
// principals suit the host being connected to — see the phase 5 plan's open
// question. Errors are treated as "nothing to reuse": this is an
// optimization, and failing it should mean getting a fresh certificate, not
// failing the login.
func reusableCertificate(agent sshagent.Agent) *ssh.Certificate {
	certs, err := agent.Certificates()
	if err != nil {
		return nil
	}

	cutoff := uint64(time.Now().Add(reuseGrace).Unix()) //nolint:gosec // a Unix timestamp is positive for any real date
	for _, cert := range certs {
		if cert != nil && cert.ValidBefore > cutoff {
			return cert
		}
	}
	return nil
}

// openBrowser makes a best-effort attempt to open url. A failure is
// reported and otherwise ignored: the URL has already been printed, and a
// machine with no browser (a headless jump box, a CI runner) must still be
// able to complete a login by hand.
func openBrowser(ctx context.Context, out io.Writer, url string) {
	ctx, cancel := context.WithTimeout(ctx, browserLaunchTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "(could not open a browser automatically: %v)\n", err)
	}
}

// principalList renders a certificate's principals for a human, e.g. "alice"
// or "alice, root".
func principalList(cert *ssh.Certificate) string {
	if cert == nil || len(cert.ValidPrincipals) == 0 {
		return "no principals"
	}
	out := cert.ValidPrincipals[0]
	for _, p := range cert.ValidPrincipals[1:] {
		out += ", " + p
	}
	return out
}

// expiryPhrase renders when a certificate runs out, in both the terms a user
// thinks in: the wall-clock time and how long that is from now. "valid until
// 18:04 (7h58m from now)" answers "do I need to log in again before I finish
// this?" without any arithmetic.
func expiryPhrase(cert *ssh.Certificate) string {
	if cert == nil || cert.ValidBefore == 0 {
		return "with no expiry"
	}

	expires := time.Unix(int64(cert.ValidBefore), 0) //nolint:gosec // ValidBefore is a Unix timestamp set by the server
	remaining := time.Until(expires).Round(time.Minute)
	if remaining <= 0 {
		return "already expired"
	}
	return fmt.Sprintf("valid until %s (%s from now)", expires.Local().Format("15:04"), remaining)
}
