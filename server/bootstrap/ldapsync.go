package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/logging"
	"github.com/mnestor/ssoossh/server/service"
)

// DirectorySync is the directory sync wired for one run from the command
// line (`ssoosshd ldap sync`): the services the admin console's "Sync now"
// drives, built against the server's own database without the rest of the
// server (no HTTP listener, no scheduler, no broker, no signer).
//
// Running it beside a live ssoosshd is what every multi-instance deployment
// already does, because the scheduled syncs are not leader-gated (see
// service.RunManualSync for what keeps concurrent passes safe). On SQLite
// the second process is a second writer to the same file; connectDatabase
// opens it in WAL mode with a busy timeout and immediate transactions, so a
// write waits for the server's rather than failing. A write that still
// times out is logged and leaves that user as it was: a failed write never
// disables anyone.
type DirectorySync struct {
	ldap  *service.LDAPService
	nss   *service.NSSDirectoryService
	audit *service.AuditService

	// closers release the database pool and the rotating log files, in
	// reverse order of opening.
	closers []func(context.Context) error
}

// OpenDirectorySync builds the directory sync from c. The process log goes
// to console (and to the configured files, the audit log included), so
// stdout stays the command's own. ldap.enabled must be on.
//
// The database is opened as the server opens it, but never migrated: the
// schema must already be the one this build expects (checkSchemaCurrent).
// Mail, when mail.enabled is on, is delivered by this process before the
// run returns, through the same templates and relay as the server's.
func OpenDirectorySync(c *config.Config, console io.Writer) (*DirectorySync, error) {
	if !c.LDAP.Enabled {
		return nil, errors.New("ldap.enabled is false in this configuration, so there is no directory sync to run")
	}

	closeLogs, err := logging.NewWithConsole(c, console)
	if err != nil {
		return nil, err
	}
	d := &DirectorySync{closers: closeLogs}

	if err := d.open(c); err != nil {
		_ = d.Close(context.Background())
		return nil, err
	}
	return d, nil
}

// open connects the database and builds the services, wired as
// initServices wires them for the sync's purposes.
func (d *DirectorySync) open(c *config.Config) error {
	db, err := connectDatabase(c)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	d.closers = append(d.closers, func(context.Context) error {
		sqlDB, err := db.DB()
		if err != nil {
			// not covered: db came from gorm.Open over a *sql.DB pool,
			// which DB() always returns.
			return err
		}
		return sqlDB.Close()
	})
	if err := checkSchemaCurrent(c.DB.Provider, db); err != nil {
		return err
	}

	a := &app{config: c, db: db}
	d.audit = service.NewAuditService(c, db)
	if d.ldap, err = service.NewLDAPService(c, db); err != nil {
		return err
	}
	if d.nss, err = service.NewNSSDirectoryService(c, db); err != nil {
		return err
	}
	d.ldap.SetAuditor(d.audit)
	d.nss.SetAuditor(d.audit)
	d.ldap.SetNSSDirectory(d.nss)

	// The pass emails an admin it disables, always. Delivered inline: this
	// process has no router to consume a queue, and on NATS a consumer of
	// its own would take mail meant for the running servers.
	if c.Mail.Enabled {
		handler, err := a.newNotificationHandler()
		if err != nil {
			return err
		}
		d.ldap.SetNotifier(service.NewNotificationService(db, handler.InlinePublisher(), true,
			c.LDAP.Enabled, c.CertOptions.Service.AllowUserAccounts))
	}
	return nil
}

// Run runs one manual pass, as POST /api/admin/ldap/sync does.
func (d *DirectorySync) Run(ctx context.Context, req service.ManualSyncRequest) *service.ManualSyncResult {
	// An untyped nil when NSS is off, so RunManualSync sees no NSS sync
	// rather than a nil pointer behind the interface.
	var nss service.NSSDirectorySync
	if d.nss != nil {
		nss = d.nss
	}
	return service.RunManualSync(ctx, d.ldap, nss, d.audit, req)
}

// Close releases the database and the log files, joining their errors.
func (d *DirectorySync) Close(ctx context.Context) error {
	var errs []error
	for i := len(d.closers) - 1; i >= 0; i-- {
		if err := d.closers[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	d.closers = nil
	return errors.Join(errs...)
}
