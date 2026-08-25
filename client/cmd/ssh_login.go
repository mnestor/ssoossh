package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

	"github.com/mnestor/ssoossh/client/config"
)

// loginExtensions are the certificate extensions `ssh login` asks for by
// default. The server narrows this against its own configuration and the web
// UI shows what survived before anyone approves (see docs/internals/invariants.md, Hard
// Constraints), so asking for the full interactive set is a request, not a
// demand — anything the deployment does not permit is simply trimmed.
//
// Users can opt out of extensions via config settings or CLI flags, and
// administrators can forbid extensions via policy (Windows registry, macOS
// managed preferences, or the enforce file on Linux). The effective set is
// the default, minus config opt-outs, minus flag opt-outs, minus policy
// forbidden — in that order. Asking for nothing is not an option: narrowing
// is an intersection, so an empty request yields a certificate carrying no
// extensions at all, which cannot open an interactive session.
var loginExtensions = []string{
	"permit-pty",
	"permit-agent-forwarding",
	"permit-port-forwarding",
	"permit-X11-forwarding",
	"permit-user-rc",
}

// extensionToConfig maps extension names to their config field addresses,
// used by effectiveExtensions to check opt-out settings.
var extensionToConfig = map[string]func(*config.CertificateExtensionOptions) bool{
	"permit-pty":              func(c *config.CertificateExtensionOptions) bool { return c.NoPTY },
	"permit-agent-forwarding": func(c *config.CertificateExtensionOptions) bool { return c.NoAgentForwarding },
	"permit-port-forwarding":  func(c *config.CertificateExtensionOptions) bool { return c.NoPortForwarding },
	"permit-X11-forwarding":   func(c *config.CertificateExtensionOptions) bool { return c.NoX11Forwarding },
	"permit-user-rc":          func(c *config.CertificateExtensionOptions) bool { return c.NoUserRC },
}

// extensionToViperKey maps extension names to the config key their opt-out
// lives under, which is also the key the corresponding --no-* flag binds to
// (see bindFlags in client/config). effectiveExtensions uses it to look the
// extension up in Config.SetByFlag and so report which layer removed it.
//
// Kept beside extensionToConfig above rather than derived from it: the two
// have to stay in step, and a name typed twice next to each other is easier
// to keep honest than one computed from a struct field name.
var extensionToViperKey = map[string]string{
	"permit-pty":              "certificate_extensions.no_pty",
	"permit-agent-forwarding": "certificate_extensions.no_agent_forwarding",
	"permit-port-forwarding":  "certificate_extensions.no_port_forwarding",
	"permit-X11-forwarding":   "certificate_extensions.no_x11_forwarding",
	"permit-user-rc":          "certificate_extensions.no_user_rc",
}

// reuseGrace is how much validity a already-loaded certificate must have
// left before `ssh login` will hand it back instead of getting a new one.
// Without it a certificate expiring in the next instant counts as reusable
// and the SSH connection it was reused for fails moments later.
const reuseGrace = 10 * time.Second

// browserLaunchTimeout bounds the best-effort browser launch. The URL is
// always printed first, so a launcher that hangs must not hold up the login.
const browserLaunchTimeout = 5 * time.Second

// extensionRemovalReason tracks why an extension was removed from the
// requested set, for the effective-request summary line.
type extensionRemovalReason int

const (
	removed_config extensionRemovalReason = iota
	removed_flag
	removed_policy
)

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
			// Certificate extension flags: opt out of requested extensions.
			// Bound to certificate_extensions.* in config.go's newConfig.
			cd.CobraCommand.Flags().Bool("no-pty", false,
				"do not request permit-pty extension")
			cd.CobraCommand.Flags().Bool("no-agent-forwarding", false,
				"do not request permit-agent-forwarding extension")
			cd.CobraCommand.Flags().Bool("no-port-forwarding", false,
				"do not request permit-port-forwarding extension")
			cd.CobraCommand.Flags().Bool("no-x11-forwarding", false,
				"do not request permit-X11-forwarding extension")
			cd.CobraCommand.Flags().Bool("no-user-rc", false,
				"do not request permit-user-rc extension")
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

// emptyExtensionSetError explains that nothing is left to request, naming
// the layer the reader can actually do something about.
//
// Policy outranks a flag and a flag outranks config, so the highest reason
// present is the one reported: telling someone to edit a config file when
// policy forbids the extension outright wastes the one instruction they
// get, and telling them to edit a file when they typed a flag sends them to
// the wrong place entirely.
func emptyExtensionSetError(removals map[string]extensionRemovalReason) error {
	reason := removed_config
	for _, r := range removals {
		if r > reason {
			reason = r
		}
	}

	switch reason {
	case removed_config:
		return errors.New("all certificate extensions were opted out via configuration; cannot issue an unusable certificate")
	case removed_flag:
		return errors.New("all certificate extensions were opted out via command-line flags; cannot issue an unusable certificate")
	case removed_policy:
		return errors.New("all certificate extensions are forbidden by policy; cannot issue an unusable certificate")
	default:
		// not covered: reason comes from the three constants above and the
		// loop only ever raises it, so there is no fourth value to reach
		// here short of a new constant being added without a case.
		return fmt.Errorf("all certificate extensions removed by unknown reason; cannot issue an unusable certificate")
	}
}

// effectiveExtensions computes which extensions to request, applying
// config opt-outs, flag opt-outs, and policy forbidding in order. It
// returns the effective extensions list and an attribution map showing
// which layer (config, flag, or policy) removed each extension that was
// removed. It returns an error if the result is empty (an unusable
// certificate).
func effectiveExtensions(cfg *config.Config, removals map[string]extensionRemovalReason) ([]string, error) {
	// Start with the full set.
	requested := make(map[string]bool)
	for _, ext := range loginExtensions {
		requested[ext] = true
	}

	// Apply config opt-outs, attributing each to the layer the user can
	// actually change. A flag and a config file reach this struct the same
	// way -- bindFlags binds --no-pty to certificate_extensions.no_pty --
	// so cfg.SetByFlag is the only thing that distinguishes them, and
	// without it someone who just typed --no-pty is told their
	// configuration did it.
	for ext, isOptedOut := range extensionToConfig {
		if isOptedOut(&cfg.CertificateExtensions) {
			delete(requested, ext)
			removals[ext] = removed_config
			if cfg.SetByFlag[extensionToViperKey[ext]] {
				removals[ext] = removed_flag
			}
		}
	}

	// Apply policy-forbidden (unconditionally, even if a flag tried to re-add it).
	for _, forbidden := range cfg.ForbiddenCertificateExtensions {
		if requested[forbidden] {
			delete(requested, forbidden)
			removals[forbidden] = removed_policy
		} else if _, inRemoved := removals[forbidden]; inRemoved {
			// If it was already removed by config, overwrite to show policy as the reason.
			removals[forbidden] = removed_policy
		} else {
			// If it wasn't in the default set at all, still record it for clarity.
			removals[forbidden] = removed_policy
		}
	}

	// Guard against empty set.
	if len(requested) == 0 {
		return nil, emptyExtensionSetError(removals)
	}

	// Convert back to a sorted slice.
	result := make([]string, 0, len(requested))
	for ext := range requested {
		result = append(result, ext)
	}
	return result, nil
}

// printEffectiveExtensions outputs a summary of which extensions are being
// requested and which were removed by which layer (config, flag, or policy).
func printEffectiveExtensions(out io.Writer, effective []string, allDefault []string, removals map[string]extensionRemovalReason) {
	fmt.Fprintf(out, "Requesting certificate extensions: %v (removed: ", effective)
	var first bool
	for _, ext := range allDefault {
		if reason, removed := removals[ext]; removed {
			if first {
				fmt.Fprint(out, ", ")
			}
			reasonStr := "config"
			switch reason {
			case removed_flag:
				reasonStr = "flag"
			case removed_policy:
				reasonStr = "policy"
			}
			fmt.Fprintf(out, "%s(%s)", ext, reasonStr)
			first = true
		}
	}
	fmt.Fprintf(out, ")\n\n")
}

// runLoginPreflight verifies key storage will work, with optional fallback logic.
// It probes the resolved agent to ensure it can store and release a keypair,
// and if the probe fails with fallback enabled, attempts to use file storage instead.
func runLoginPreflight(root *RootCommand, cfg *config.Config, out io.Writer) error {
	agent := root.Agent()
	var preflightErr error
	if agent.Type() == sshagent.AgentTypeSsh {
		preflightErr = probeAgentPreflight(agent)
	} else if agent.Type() == sshagent.AgentTypeFile {
		preflightErr = probeFileAgentPreflightWithPath(cfg.Filename)
	}
	// For any other agent type (e.g., test fakes), skip the preflight

	if preflightErr == nil {
		return nil
	}

	slog.Debug("agent preflight failed", "error", preflightErr)

	// If preflight failed and fallback is configured, try the file agent
	if cfg.UseAgent && cfg.FallbackFileAgent {
		slog.Warn("agent preflight failed, attempting fallback to file storage", "error", preflightErr)
		fallbackErr := probeFileAgentPreflightWithPath(cfg.Filename)
		if fallbackErr != nil {
			return fmt.Errorf("agent storage check failed and fallback also failed: primary error: %w, fallback error: %w", preflightErr, fallbackErr)
		}
		fileAgent, err := root.newFileAgent(cfg.Filename)
		if err != nil {
			return fmt.Errorf("agent storage check failed and fallback is unavailable: %w (fallback error: %w)", preflightErr, err)
		}
		if setCAErr := fileAgent.SetCA(cfg.CAPubkey); setCAErr != nil {
			return fmt.Errorf("agent storage check failed and fallback setup failed: %w (setup error: %w)", preflightErr, setCAErr)
		}
		// Fallback succeeded, replace the agent
		root.ssh = fileAgent
		fmt.Fprintf(out, "Agent storage check failed, falling back to file-based key storage.\n")
		return nil
	}

	// No fallback or fallback disabled
	if cfg.UseAgent && !cfg.FallbackFileAgent {
		return fmt.Errorf("cannot verify agent key storage will work: %w (fallback is disabled)", preflightErr)
	}
	return fmt.Errorf("cannot verify key storage will work: %w", preflightErr)
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

	// Preflight: verify the resolved key storage will accept and release a key
	// before we request a certificate. This prevents the "approve then lose it"
	// hazard where a human approves but the certificate vanishes because storage fails.
	if err := runLoginPreflight(root, cfg, out); err != nil {
		return err
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

	// Compute effective extensions with attribution.
	removals := make(map[string]extensionRemovalReason)
	effectiveExts, err := effectiveExtensions(cfg, removals)
	if err != nil {
		return err
	}

	// Show what's being requested if it differs from the default.
	if len(removals) > 0 {
		printEffectiveExtensions(out, effectiveExts, loginExtensions, removals)
	}

	pending, err := root.API().CreateUserRequest(ctx, publicKey, localUsername, localHostname, api.RequestedOptions{
		Extensions:      effectiveExts,
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
	slog.Info("waiting for approval", "request", pending.RequestID)

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
	slog.Info("certificate issued", "principals", principalList(kp.Certificate()), "expiry", expiryPhrase(kp.Certificate()))
	slog.Debug("storing the certificate", "backend", root.Agent().Backend())
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
// request actually came from. Best-effort: either value is
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
