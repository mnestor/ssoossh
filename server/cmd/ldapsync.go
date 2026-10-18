package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/server/bootstrap"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/controller"
	"github.com/mnestor/ssoossh/server/service"
)

var _ simplecobra.Commander = (*ldapSyncCommand)(nil)

// directorySyncer is what the sync command runs a pass on and releases.
// *bootstrap.DirectorySync in the shipped command; a fake in tests.
type directorySyncer interface {
	Run(ctx context.Context, req service.ManualSyncRequest) *service.ManualSyncResult
	Close(ctx context.Context) error
}

// openDirectorySync builds the syncer for a loaded config, with the process
// log on console.
type openDirectorySync func(cfg *config.Config, console io.Writer) (directorySyncer, error)

// ldapSyncCommand runs the directory sync now, in this process.
//
// The admin console's "Sync now" needs a browser session, and a restart
// does not help because the scheduled syncs do not run at startup; this is
// the same pass from a shell or a script. Whoever can run it can already
// read the config file, and so the database credentials and the bind
// password.
type ldapSyncCommand struct {
	dryRun bool
	asJSON bool

	// open builds the syncer; bootstrap.OpenDirectorySync unless a test
	// replaced it.
	open openDirectorySync
}

func newLDAPSyncCommand() simplecobra.Commander {
	return &ldapSyncCommand{open: func(cfg *config.Config, console io.Writer) (directorySyncer, error) {
		return bootstrap.OpenDirectorySync(cfg, console)
	}}
}

// Name implements simplecobra.Commander.
func (c *ldapSyncCommand) Name() string { return "sync" }

// Commands implements simplecobra.Commander.
func (c *ldapSyncCommand) Commands() []simplecobra.Commander { return nil }

// Init implements simplecobra.Commander.
func (c *ldapSyncCommand) Init(cd *simplecobra.Commandeer) error {
	cmd := cd.CobraCommand
	cmd.Short = "Run the directory sync now, as the admin console's Sync now does."
	cmd.Long = "Runs, in this process, the pass POST /api/admin/ldap/sync runs: the LDAP " +
		"enrichment sync over every user who has logged in with the directory on, which " +
		"refreshes their directory data and applies ldap.sync.disable_after and " +
		"ldap.sync.reenable; then, with nss.enabled on, the NSS directory's full sync. It " +
		"reports what the pass concluded and records an ldap.sync_triggered audit event " +
		"whose actor is local-cli.\n\n" +
		"With --dry-run the LDAP pass reads the directory and changes nothing, reporting the " +
		"disables and re-enables it would have performed, and the NSS sync is skipped: it " +
		"has no dry run.\n\n" +
		"Safe beside a running server: the scheduled syncs are not leader-elected, so every " +
		"instance already runs its own, and this is one more. A restart does not run a " +
		"sync; the scheduled ones first run one interval after start.\n\n" +
		"The database is the server's, opened from the same config file, and is never " +
		"migrated: its schema must match this ssoosshd. Exits non-zero when either pass " +
		"fails, with the reason on stderr; the process log goes to stderr too."

	// No arguments: `ldap sync dry-run`, a mistyped flag, must not run a
	// live pass.
	cmd.Args = cobra.NoArgs
	cmd.Flags().BoolVar(&c.dryRun, "dry-run", false, "read the directory and change nothing; skips the NSS sync")
	cmd.Flags().BoolVar(&c.asJSON, "json", false, "print the result as JSON, in the sync endpoint's response shape")
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *ldapSyncCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander.
func (c *ldapSyncCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	cfg, err := config.NewConfig(cd.CobraCommand)
	if err != nil {
		return err
	}
	if !cfg.LDAP.Enabled {
		return errors.New("ldap.enabled is false in this configuration, so there is no directory sync to run")
	}

	syncer, err := c.open(cfg, cd.CobraCommand.ErrOrStderr())
	if err != nil {
		return err
	}
	// Reported on stderr directly: the log files are among what is being
	// closed.
	defer func() {
		if err := syncer.Close(context.Background()); err != nil {
			_, _ = fmt.Fprintln(cd.CobraCommand.ErrOrStderr(), "ssoosshd: failed to release the database or the log files after the directory sync:", err)
		}
	}()

	result := syncer.Run(ctx, service.ManualSyncRequest{
		DryRun: c.dryRun,
		Actor:  service.AuditSubjectForCLI(),
		Detail: cliAuditDetail(),
	})

	out := cd.CobraCommand.OutOrStdout()
	if c.asJSON {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(controller.NewManualSyncResponse(result)); err != nil {
			return err
		}
	} else if err := writeSyncReport(out, result, cfg.NSS.Enabled, c.dryRun); err != nil {
		return err
	}
	return syncOutcomeError(result)
}

// cliAuditDetail says who ran the command and where, for the audit event:
// the operating-system account, the account sudo was run from when it was,
// and the host. Context, not proof: they are what the environment tells
// this process, and whoever runs it could set SUDO_USER.
func cliAuditDetail() map[string]any {
	detail := map[string]any{"via": "cli"}
	if u, err := user.Current(); err == nil {
		detail["os_user"] = u.Username
	}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		detail["sudo_user"] = sudoUser
	}
	if host, err := os.Hostname(); err == nil {
		detail["host"] = host
	}
	return detail
}

// syncOutcomeError is the command's exit status: nil when both passes
// completed, whatever they concluded about individual entries, and the
// reason otherwise.
func syncOutcomeError(result *service.ManualSyncResult) error {
	var errs []error
	if result.Err != nil {
		errs = append(errs, fmt.Errorf("the directory sync failed: %w", result.Err))
	}
	if result.NSSErr != nil {
		errs = append(errs, fmt.Errorf("the NSS directory sync failed: %w", result.NSSErr))
	}
	return errors.Join(errs...)
}

// writeSyncReport prints what the pass concluded, one fact per line with
// a fixed label, so `grep '^disabled'` keeps working.
func writeSyncReport(out io.Writer, result *service.ManualSyncResult, nssEnabled, dryRun bool) error {
	w := &probeWriter{out: out}

	run := result.Run
	switch {
	case run == nil:
		w.printf("%-14s not run\n", "ldap sync")
	case run.ErrorMessage != "":
		w.printf("%-14s failed (run %s on %s)\n", "ldap sync", run.ID, orUnknown(run.Instance))
		w.printf("%-14s %s\n", "ldap error", run.ErrorMessage)
	default:
		w.printf("%-14s completed (run %s on %s)\n", "ldap sync", run.ID, orUnknown(run.Instance))
	}
	if run != nil {
		if run.DryRun {
			w.printf("%-14s yes: nothing was changed; disabled and re-enabled are what the pass would have done\n", "dry run")
		} else {
			w.printf("%-14s no\n", "dry run")
		}
		w.printf("%-14s %d\n", "users", run.UsersSeen)
		w.printf("%-14s %d\n", "found", run.Found)
		w.printf("%-14s %d\n", "missing", run.Missing)
		w.printf("%-14s %d\n", "failed", run.Failed)
		w.printf("%-14s %d\n", "disabled", run.Disabled)
		w.printf("%-14s %d\n", "re-enabled", run.Reenabled)
	}

	writeNSSReport(w, result, nssEnabled, dryRun)
	return w.err
}

// writeNSSReport prints the NSS half: why it did not run, or what it
// counted for the accounts and the groups.
func writeNSSReport(w *probeWriter, result *service.ManualSyncResult, nssEnabled, dryRun bool) {
	switch {
	case !nssEnabled:
		w.printf("%-14s not run: nss.enabled is false\n", "nss sync")
		return
	case dryRun:
		w.printf("%-14s not run: it has no dry run\n", "nss sync")
		return
	case !result.NSSRan:
		w.printf("%-14s not run\n", "nss sync")
		return
	}

	if result.NSSErr != nil {
		w.printf("%-14s failed\n", "nss sync")
		w.printf("%-14s %s\n", "nss error", result.NSSErr)
	} else {
		w.printf("%-14s completed\n", "nss sync")
	}
	if result.NSS == nil {
		return
	}
	for _, table := range []struct {
		label  string
		counts service.NSSSyncCounts
	}{
		{"nss accounts", result.NSS.Accounts},
		{"nss groups", result.NSS.Groups},
	} {
		c := table.counts
		w.printf("%-14s seen=%d created=%d updated=%d skipped=%d refused=%d failed=%d missing=%d tombstoned=%d\n",
			table.label, c.Seen, c.Created, c.Updated, c.Skipped, c.Refused, c.Failed, c.Missing, c.Tombstoned)
	}
}

// orUnknown names an instance the run row could not record.
func orUnknown(instance string) string {
	if instance == "" {
		return "an unknown host"
	}
	return instance
}
