package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/server/bootstrap"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

var (
	_ simplecobra.Commander = (*identityCommand)(nil)
	_ simplecobra.Commander = (*identityEchoCommand)(nil)
)

// identityCommand groups the identity tools.
type identityCommand struct {
	commands []simplecobra.Commander
}

func newIdentityCommand() simplecobra.Commander {
	return &identityCommand{commands: []simplecobra.Commander{newIdentityEchoCommand()}}
}

// Name implements simplecobra.Commander.
func (c *identityCommand) Name() string { return "identity" }

// Commands implements simplecobra.Commander.
func (c *identityCommand) Commands() []simplecobra.Commander { return c.commands }

// Init implements simplecobra.Commander.
func (c *identityCommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Identity provider tools."
	cd.CobraCommand.Long = "Tools for the OIDC sign-in, working against the loaded configuration " +
		"file and the server's database. echo mints a one-time link that shows whoever opens " +
		"it the claims their identity provider sends."
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *identityCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander. The bare `identity` command prints
// help.
func (c *identityCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return cd.CobraCommand.Help()
}

// echoCodeStore is what the echo command mints a code in and watches.
// *bootstrap.IdentityEcho in the shipped command; a fake in tests.
type echoCodeStore interface {
	Mint(ctx context.Context, req service.MintRequest) (*service.MintedEchoCode, error)
	Get(ctx context.Context, id string) (*model.IdentityEchoCode, error)
	End(ctx context.Context, id, reason string, actor *service.AuditSubject) (bool, error)
	Close(ctx context.Context) error
}

// openEchoCodeStore builds the store for a loaded config, with the process
// log on console.
type openEchoCodeStore func(cfg *config.Config, console io.Writer) (echoCodeStore, error)

// echoCompletionWindow is how long the command waits, once the link is
// used, for the sign-in to come back to the callback. A person can be slow
// at a provider's login page, but not this slow.
const echoCompletionWindow = 10 * time.Minute

// identityEchoCommand mints a bootstrap identity echo link and reports how
// it was used.
//
// The admin claims echo needs a sign-in that already works and an admin
// role that is already detected, which is exactly what is missing at first
// setup. Host access is the authority that exists then: whoever can run
// this can already read the config and open the database. The link hands
// one browser exactly one echo of its own claims, and nothing else.
type identityEchoCommand struct {
	ttl time.Duration

	// open builds the store; bootstrap.OpenIdentityEcho unless a test
	// replaced it. poll is how often the row is read.
	open openEchoCodeStore
	poll time.Duration
	now  func() time.Time
}

func newIdentityEchoCommand() simplecobra.Commander {
	return &identityEchoCommand{
		open: func(cfg *config.Config, console io.Writer) (echoCodeStore, error) {
			return bootstrap.OpenIdentityEcho(cfg, console)
		},
		poll: 2 * time.Second,
		now:  time.Now,
	}
}

// Name implements simplecobra.Commander.
func (c *identityEchoCommand) Name() string { return "echo" }

// Commands implements simplecobra.Commander.
func (c *identityEchoCommand) Commands() []simplecobra.Commander { return nil }

// Init implements simplecobra.Commander.
func (c *identityEchoCommand) Init(cd *simplecobra.Commandeer) error {
	cmd := cd.CobraCommand
	cmd.Short = "Mint a one-time link that shows your identity provider's claims, for first setup."
	cmd.Long = "Mints a one-time identity echo link and prints it. Whoever opens it signs in at " +
		"the identity provider and sees the claims it sends: each authentication.fields entry " +
		"and what it resolves to, whether the sign-in would succeed, candidates for the subject, " +
		"username and groups claims and for admin.require_group, and YAML to copy or download. " +
		"No admin role is needed and no mapping has to work yet; nothing is stored, and no " +
		"session is created.\n\n" +
		"Run it on the server host as the service account, with the server running: the link " +
		"opens the server's own page. The code rides in the link's fragment, so no access log " +
		"or proxy sees it; the database holds only its SHA-256. It works once, until --ttl runs " +
		"out, and minting another replaces it.\n\n" +
		"The command then waits and prints what happened: when and from which address the link " +
		"was used and how the sign-in ended (an error from the code exchange or the token " +
		"check, when there was one), or that it expired. It never prints claims. Ctrl-C retires " +
		"an unused link. Exits non-zero unless the claims were shown.\n\n" +
		"Records auth.echo_code_minted, auth.echo_code_used and auth.echo_code_expired, whose " +
		"actor for the first is local-cli. The database is never migrated: its schema must " +
		"match this ssoosshd."

	cmd.Args = cobra.NoArgs
	cmd.Flags().DurationVar(&c.ttl, "ttl", service.DefaultEchoCodeTTL, "how long the link works")
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *identityEchoCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander.
func (c *identityEchoCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	if c.ttl <= 0 {
		return errors.New("--ttl must be positive")
	}
	cfg, err := config.NewConfig(cd.CobraCommand)
	if err != nil {
		return err
	}
	origin := cfg.HTTP.PublicOrigin()
	if origin == "" {
		return errors.New("http.public_url is not set, so there is no address to build the link from")
	}

	store, err := c.open(cfg, cd.CobraCommand.ErrOrStderr())
	if err != nil {
		return err
	}
	// Reported on stderr directly: the log files are among what is being
	// closed.
	defer func() {
		if err := store.Close(context.Background()); err != nil {
			_, _ = fmt.Fprintln(cd.CobraCommand.ErrOrStderr(), "ssoosshd: failed to release the database or the log files:", err)
		}
	}()

	detail := cliAuditDetail()
	minted, err := store.Mint(ctx, service.MintRequest{
		TTL:         c.ttl,
		RequestedBy: requestedBy(detail),
		Actor:       service.AuditSubjectForCLI(),
		Detail:      detail,
	})
	if err != nil {
		return err
	}

	w := &probeWriter{out: cd.CobraCommand.OutOrStdout()}
	w.printf("Open this link in a browser and sign in. It works once, until %s:\n\n", minted.Row.ExpiresAt.Local().Format(time.RFC1123))
	w.printf("  %s/identity/echo#code=%s\n\n", origin, minted.Code)
	w.printf("Waiting for it to be used (Ctrl-C retires it)...\n")
	if w.err != nil {
		return w.err
	}

	err = c.wait(ctx, store, minted.Row.ID, w)
	if w.err != nil {
		return w.err
	}
	return err
}

// wait watches the code's row until the echo it started has ended, the
// code expired or was replaced, or ctx is cancelled, printing each step.
func (c *identityEchoCommand) wait(ctx context.Context, store echoCodeStore, id string, w *probeWriter) error {
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()

	reportedUse := false
	for {
		row, err := store.Get(ctx, id)
		switch {
		case errors.Is(err, service.ErrEchoCodeReplaced):
			w.printf("%-9s a newer link replaced this one, which no longer works\n", "replaced")
			return err
		case err != nil && ctx.Err() == nil:
			return err
		case err == nil:
			if done, outcome := c.report(ctx, store, row, &reportedUse, w); done {
				return outcome
			}
		}

		select {
		case <-ctx.Done():
			// The interrupt retires a link nobody has used yet, so it
			// cannot outlive the command that would report on it. On a
			// fresh context: this one is already cancelled.
			retired, err := store.End(context.Background(), id, service.EchoCodeExpiredCancelled, service.AuditSubjectForCLI())
			if err == nil && retired {
				w.printf("%-9s the link no longer works\n", "cancelled")
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// report prints what row says, once per step, and reports whether the
// wait is over and with which exit status.
func (c *identityEchoCommand) report(ctx context.Context, store echoCodeStore, row *model.IdentityEchoCode, reportedUse *bool, w *probeWriter) (bool, error) {
	now := c.now()
	if row.UsedAt == nil {
		if now.Before(row.ExpiresAt) {
			return false, nil
		}
		if _, err := store.End(ctx, row.ID, service.EchoCodeExpiredTimedOut, nil); err != nil {
			return true, err
		}
		w.printf("%-9s %s, unused\n", "expired", row.ExpiresAt.Local().Format(time.RFC1123))
		return true, errors.New("the link expired unused")
	}

	if !*reportedUse {
		*reportedUse = true
		from := row.UsedFrom
		if from == "" {
			from = "an unknown address"
		}
		w.printf("%-9s at %s from %s\n", "used", row.UsedAt.Local().Format(time.RFC1123), from)
	}

	switch {
	case row.CompletedAt != nil && row.Outcome == "":
		w.printf("%-9s the claims were shown in the browser; nothing was stored\n", "outcome")
		return true, nil
	case row.CompletedAt != nil:
		w.printf("%-9s %s\n", "error", strings.TrimSpace(row.Outcome))
		return true, errors.New("the sign-in did not produce claims")
	case now.After(row.UsedAt.Add(echoCompletionWindow)):
		w.printf("%-9s the sign-in did not come back within %s\n", "outcome", echoCompletionWindow)
		return true, errors.New("the sign-in did not come back")
	}
	return false, nil
}

// requestedBy is the row's requested_by: the OS account (and the sudo
// caller when there is one) at the host.
func requestedBy(detail map[string]any) string {
	var who string
	if user, ok := detail["os_user"].(string); ok {
		who = user
	}
	if sudo, ok := detail["sudo_user"].(string); ok && sudo != "" {
		who += " (sudo from " + sudo + ")"
	}
	if host, ok := detail["host"].(string); ok && host != "" {
		who += "@" + host
	}
	return who
}
