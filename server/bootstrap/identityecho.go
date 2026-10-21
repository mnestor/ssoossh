package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/logging"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// IdentityEcho is the bootstrap identity echo's code store wired for one
// run from the command line (`ssoosshd identity echo`): the server's own
// database and audit log, without the rest of the server. The running
// server redeems the code; this process mints it and watches its row, the
// shared database being the only channel between the two, as it is for
// `ssoosshd ldap sync`.
type IdentityEcho struct {
	codes *service.IdentityEchoCodeService

	// closers release the database pool and the rotating log files, in
	// reverse order of opening.
	closers []func(context.Context) error
}

// OpenIdentityEcho builds the code store from c. The process log goes to
// console (and to the configured files, the audit log included), so stdout
// stays the command's own.
//
// The database is opened as the server opens it, but never migrated: the
// schema must already be the one this build expects (checkSchemaCurrent).
func OpenIdentityEcho(c *config.Config, console io.Writer) (*IdentityEcho, error) {
	closeLogs, err := logging.NewWithConsole(c, console)
	if err != nil {
		return nil, err
	}
	e := &IdentityEcho{closers: closeLogs}

	db, err := connectDatabase(c)
	if err != nil {
		_ = e.Close(context.Background())
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	e.closers = append(e.closers, func(context.Context) error {
		sqlDB, err := db.DB()
		if err != nil {
			// not covered: db came from gorm.Open over a *sql.DB pool,
			// which DB() always returns.
			return err
		}
		return sqlDB.Close()
	})
	if err := checkSchemaCurrent(c.DB.Provider, db); err != nil {
		_ = e.Close(context.Background())
		return nil, err
	}

	e.codes = service.NewIdentityEchoCodeService(db)
	e.codes.SetAuditor(service.NewAuditService(c, db))
	return e, nil
}

// Mint stores a new code, replacing any other.
func (e *IdentityEcho) Mint(ctx context.Context, req service.MintRequest) (*service.MintedEchoCode, error) {
	return e.codes.Mint(ctx, req)
}

// Get reads a code's row.
func (e *IdentityEcho) Get(ctx context.Context, id string) (*model.IdentityEchoCode, error) {
	return e.codes.Get(ctx, id)
}

// End retires a code that is still unused.
func (e *IdentityEcho) End(ctx context.Context, id, reason string, actor *service.AuditSubject) (bool, error) {
	return e.codes.End(ctx, id, reason, actor)
}

// Close releases the database and the log files, joining their errors.
func (e *IdentityEcho) Close(ctx context.Context) error {
	var errs []error
	for i := len(e.closers) - 1; i >= 0; i-- {
		if err := e.closers[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	e.closers = nil
	return errors.Join(errs...)
}
