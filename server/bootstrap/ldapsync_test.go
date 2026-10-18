package bootstrap

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/test/testdb"
)

// directorySyncTestConfig is a config with the directory on, over a fresh
// empty database on the run's backend: a file for SQLite, so a second pool
// (or process) sees the same data.
func directorySyncTestConfig(t *testing.T) *config.Config {
	t.Helper()

	c := &config.Config{}
	if testdb.IsPostgres(t) {
		provider, connection := testdb.Connection(t)
		c.DB.Provider, c.DB.Connection = config.DBProvider(provider), connection
	} else {
		c.DB.Provider = config.DBProviderSqlite
		c.DB.Connection = filepath.Join(t.TempDir(), "ssoossh.db")
	}
	c.LDAP = config.LDAPConfig{
		Enabled:    true,
		URL:        "ldaps://directory.test",
		BaseDN:     "ou=people,dc=test",
		UserFilter: "(uid={{.Username}})",
		Sync:       config.LDAPSync{Interval: time.Minute, DisableAfter: time.Hour},
	}
	return c
}

// migrateTestDatabase brings c's database to the current schema, as a
// server start would, and closes the pool it used.
func migrateTestDatabase(t *testing.T, c *config.Config) {
	t.Helper()

	db := openTestDatabase(t, c)
	if err := migrateDatabase(c.DB.Provider, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// openTestDatabase opens c's database as the server does, closing it when
// the test ends.
func openTestDatabase(t *testing.T, c *config.Config) *gorm.DB {
	t.Helper()

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestOpenDirectorySync_ShouldRefuseWhatItCannotRunAgainst(t *testing.T) {
	saveSlogDefault(t)

	tests := []struct {
		name    string
		mutate  func(c *config.Config)
		migrate bool
		wantErr string
	}{
		{name: "should name ldap.enabled when the directory is off", mutate: func(c *config.Config) { c.LDAP.Enabled = false }, wantErr: "ldap.enabled is false"},
		{name: "should refuse a database the server never created", wantErr: "no schema yet"},
		{name: "should refuse a user filter that does not parse", migrate: true, mutate: func(c *config.Config) { c.LDAP.UserFilter = "(uid={{.Username}" }, wantErr: "user_filter"},
		{name: "should refuse a mail relay it cannot configure", migrate: true, mutate: func(c *config.Config) {
			setEnabledMail(c)
			c.Mail.SMTP.TLS = config.MailTLSRequired
			c.Mail.SMTP.CAFile = filepath.Join(t.TempDir(), "missing-ca.pem")
		}, wantErr: "mail relay"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := directorySyncTestConfig(t)
			if tt.migrate {
				migrateTestDatabase(t, c)
			}
			if tt.mutate != nil {
				tt.mutate(c)
			}

			d, err := OpenDirectorySync(c, &bytes.Buffer{})
			if err == nil {
				_ = d.Close(context.Background())
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("OpenDirectorySync() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestOpenDirectorySync_ShouldRunAPassAgainstTheServersDatabase opens the
// sync over a database a server migrated, with mail on, and runs a pass
// over nobody: the run row and the audit event are written, the log goes to
// the console given, and no directory connection is needed.
func TestOpenDirectorySync_ShouldRunAPassAgainstTheServersDatabase(t *testing.T) {
	saveSlogDefault(t)

	c := directorySyncTestConfig(t)
	setEnabledMail(c)
	c.Logging.Level = "info"
	migrateTestDatabase(t, c)

	var console bytes.Buffer
	d, err := OpenDirectorySync(c, &console)
	if err != nil {
		t.Fatalf("OpenDirectorySync() error = %v", err)
	}
	result := d.Run(context.Background(), service.ManualSyncRequest{Actor: service.AuditSubjectForCLI()})
	if err := d.Close(context.Background()); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	if result.Err != nil || result.Run == nil || result.NSSRan {
		t.Fatalf("result = %+v, want a completed LDAP pass and no NSS sync", result)
	}
	db := openTestDatabase(t, c)
	var runs, events int64
	db.Model(&model.LDAPSyncRun{}).Where("id = ?", result.Run.ID).Count(&runs)
	db.Model(&model.AuditEvent{}).Where("action = ?", string(service.AuditLDAPSyncTriggered)).Count(&events)
	if runs != 1 || events != 1 {
		t.Errorf("run rows = %d, audit events = %d; want 1 and 1", runs, events)
	}
	if !strings.Contains(console.String(), "ldap.sync_triggered") {
		t.Errorf("the audit line did not reach the console:\n%s", console.String())
	}
}

func TestCheckSchemaCurrent_ShouldAcceptOnlyTheSchemaThisBuildExpects(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, c *config.Config, db *gorm.DB)
		wantErr string
	}{
		{name: "should refuse a database with no schema", prepare: func(*testing.T, *config.Config, *gorm.DB) {}, wantErr: "no schema yet"},
		{name: "should accept the current schema", prepare: func(t *testing.T, c *config.Config, db *gorm.DB) {
			if err := migrateDatabase(c.DB.Provider, db); err != nil {
				t.Fatalf("migrate: %v", err)
			}
		}},
		{name: "should refuse a schema one migration behind", prepare: func(t *testing.T, c *config.Config, db *gorm.DB) {
			if err := migrateDatabase(c.DB.Provider, db); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			if err := withMigrator(c.DB.Provider, db, func(m *migrate.Migrate, _ uint) error { return m.Steps(-1) }); err != nil {
				t.Fatalf("step down: %v", err)
			}
		}, wantErr: "this ssoosshd expects"},
		{name: "should refuse a schema a migration left dirty", prepare: func(t *testing.T, c *config.Config, db *gorm.DB) {
			if err := migrateDatabase(c.DB.Provider, db); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			if err := db.Exec("UPDATE schema_migrations SET dirty = ?", true).Error; err != nil {
				t.Fatalf("mark dirty: %v", err)
			}
		}, wantErr: "dirty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := directorySyncTestConfig(t)
			db := openTestDatabase(t, c)
			tt.prepare(t, c, db)

			err := checkSchemaCurrent(c.DB.Provider, db)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("checkSchemaCurrent() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("checkSchemaCurrent() = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// Environment of the write-lock helper process.
const (
	sqliteLockHelperEnv = "SSOOSSH_TEST_SQLITE_LOCK_HELPER"
	sqliteLockDSNEnv    = "SSOOSSH_TEST_SQLITE_LOCK_DSN"
	sqliteLockHoldEnv   = "SSOOSSH_TEST_SQLITE_LOCK_HOLD"
)

// TestSQLiteWriteLockHelper is not a test: run as a child process by
// TestSQLite_ShouldLetASecondProcessWriteBesideTheServer, it opens the
// database as the server does, takes the write lock with a write
// transaction, says "locked" on stdout, and holds it for the given time.
func TestSQLiteWriteLockHelper(t *testing.T) {
	if os.Getenv(sqliteLockHelperEnv) != "1" {
		t.Skip("only runs as the write-lock helper process")
	}
	hold, err := time.ParseDuration(os.Getenv(sqliteLockHoldEnv))
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = os.Getenv(sqliteLockDSNEnv)
	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.LDAPSyncRun{ID: "held", StartedAt: time.Now(), Trigger: model.LDAPSyncTriggerSchedule}).Error; err != nil {
			return err
		}
		fmt.Println("locked")
		time.Sleep(hold)
		return nil
	})
	if err != nil {
		t.Fatalf("hold the write lock: %v", err)
	}
}

// TestSQLite_ShouldLetASecondProcessWriteBesideTheServer is the evidence
// that `ssoosshd ldap sync` can run beside a live server on SQLite. A
// child process holds the write lock, as a server's write would; this
// process, opened the same way, still reads at once (WAL), and its write
// waits out the lock within the busy timeout rather than failing. Past the
// timeout the write fails with "database is locked", which the sync logs
// and survives without changing that user.
func TestSQLite_ShouldLetASecondProcessWriteBesideTheServer(t *testing.T) {
	tests := []struct {
		name      string
		dsnSuffix string
		hold      time.Duration
		wantErr   bool
	}{
		{name: "should wait out the lock when it is held for less than the busy timeout", hold: 500 * time.Millisecond},
		{name: "should fail the write when the lock outlasts the busy timeout", dsnSuffix: "?_pragma=busy_timeout(100)", hold: 1500 * time.Millisecond, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &config.Config{}
			c.DB.Provider = config.DBProviderSqlite
			c.DB.Connection = filepath.Join(t.TempDir(), "ssoossh.db") + tt.dsnSuffix
			migrateTestDatabase(t, c)

			child := exec.Command(os.Args[0], "-test.run=^TestSQLiteWriteLockHelper$") //nolint:gosec // os.Args[0] is this test binary; the helper-process pattern has no other way to name it
			child.Env = append(os.Environ(), sqliteLockHelperEnv+"=1", sqliteLockDSNEnv+"="+c.DB.Connection, sqliteLockHoldEnv+"="+tt.hold.String())
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			if err := child.Start(); err != nil {
				t.Fatalf("start the helper: %v", err)
			}
			t.Cleanup(func() { _ = child.Wait() })
			waitForLine(t, stdout, "locked")

			db := openTestDatabase(t, c)
			var count int64
			readStart := time.Now()
			if err := db.Model(&model.LDAPSyncRun{}).Count(&count).Error; err != nil || time.Since(readStart) > 200*time.Millisecond {
				t.Errorf("read beside the held lock: err %v after %s, want an immediate read", err, time.Since(readStart))
			}

			start := time.Now()
			err = db.Create(&model.LDAPSyncRun{ID: "cli-" + strconv.FormatBool(tt.wantErr), StartedAt: time.Now(), Trigger: model.LDAPSyncTriggerManual}).Error
			waited := time.Since(start)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "locked") {
					t.Errorf("write past the busy timeout: err = %v, want database is locked", err)
				}
				return
			}
			if err != nil || waited < 200*time.Millisecond {
				t.Errorf("write beside the held lock: err %v after %s, want it to wait for the lock and succeed", err, waited)
			}
		})
	}
}

// waitForLine reads r until a line equal to want, failing after a few
// seconds.
func waitForLine(t *testing.T, r io.Reader, want string) {
	t.Helper()

	found := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			if scanner.Text() == want {
				found <- true
				return
			}
		}
		found <- false
	}()
	select {
	case ok := <-found:
		if !ok {
			t.Fatalf("the helper exited without printing %q", want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for the helper to print %q", want)
	}
}
