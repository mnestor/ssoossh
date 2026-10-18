package cache

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// userRow is the users table: one row per cached user, unique on name and
// on uid.
type userRow struct {
	Name      string    `gorm:"primaryKey"`
	UID       uint32    `gorm:"column:uid;uniqueIndex;not null"`
	GID       uint32    `gorm:"column:gid;not null"`
	Data      []byte    `gorm:"not null"`
	FetchedAt time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null"`
	Stale     bool      `gorm:"not null"`
}

// TableName names the table.
func (userRow) TableName() string { return "users" }

// groupRow is the groups table, unique on name and on gid. The user to gid
// list lives in each user's JSON, which is what GetMemberships reads, so
// it needs no table of its own.
type groupRow struct {
	Name      string    `gorm:"primaryKey"`
	GID       uint32    `gorm:"column:gid;uniqueIndex;not null"`
	Data      []byte    // nil for an entry filled from a user's memberships
	Members   bool      `gorm:"not null"`
	FetchedAt time.Time `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null"`
	Stale     bool      `gorm:"not null"`
}

// TableName names the table.
func (groupRow) TableName() string { return "groups" }

// SQLiteStore is the cache_mode: persistent Store, a SQLite file in WAL
// mode through the pure-Go driver, so the daemon builds with
// CGO_ENABLED=0. A host rebooted while the server is down still resolves
// the users it knew.
type SQLiteStore struct {
	db *gorm.DB
}

var _ Store = (*SQLiteStore)(nil)

// OpenSQLite opens, creating if needed, the cache database at path. The
// directory is created 0700 and the file kept 0600: nobody but root reads
// the cache, and file permissions are the trust boundary.
func OpenSQLite(ctx context.Context, path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create the cache directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the cache: %w", err)
	}
	if err := errors.Join(f.Chmod(0o600), f.Close()); err != nil {
		// not covered: chmod and close of a file this process just opened
		// read-write cannot fail short of an I/O error.
		return nil, fmt.Errorf("open the cache: %w", err)
	}

	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: q.Encode()}).String()

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, fmt.Errorf("open the cache: %w", err)
	}
	s := &SQLiteStore{db: db}
	sqlDB, err := db.DB()
	if err != nil {
		// not covered: gorm.DB.DB fails only for a connection pool that
		// is not a *sql.DB, which the sqlite dialector always is.
		return nil, fmt.Errorf("open the cache: %w", err)
	}
	// One connection: lookups take microseconds, and a single writer
	// never meets SQLITE_BUSY.
	sqlDB.SetMaxOpenConns(1)

	if err := db.WithContext(ctx).AutoMigrate(&userRow{}, &groupRow{}); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("prepare the cache: %w", err)
	}
	// The write-ahead log and its index hold the same data; SQLite
	// creates them with the process umask, so tighten them too.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			// not covered: chmod of a file SQLite just created as this
			// process cannot be refused.
			_ = s.Close()
			return nil, fmt.Errorf("protect the cache: %w", err)
		}
	}
	return s, nil
}

// UserByName implements Store.
func (s *SQLiteStore) UserByName(ctx context.Context, name string) (*UserEntry, error) {
	return s.user(ctx, "name = ?", name)
}

// UserByUID implements Store.
func (s *SQLiteStore) UserByUID(ctx context.Context, uid uint32) (*UserEntry, error) {
	return s.user(ctx, "uid = ?", uid)
}

// user reads one user row.
func (s *SQLiteStore) user(ctx context.Context, where string, arg any) (*UserEntry, error) {
	var row userRow
	err := s.db.WithContext(ctx).Where(where, arg).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the user cache: %w", err)
	}
	e := UserEntry(row)
	return &e, nil
}

// PutUser implements Store.
func (s *SQLiteStore) PutUser(ctx context.Context, e UserEntry) error {
	row := userRow(e)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("name = ? OR uid = ?", e.Name, e.UID).Delete(&userRow{}).Error; err != nil {
			return err
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		return fmt.Errorf("write the user cache: %w", err)
	}
	return nil
}

// DeleteUser implements Store.
func (s *SQLiteStore) DeleteUser(ctx context.Context, name string) error {
	if err := s.db.WithContext(ctx).Where("name = ?", name).Delete(&userRow{}).Error; err != nil {
		return fmt.Errorf("write the user cache: %w", err)
	}
	return nil
}

// GroupByName implements Store.
func (s *SQLiteStore) GroupByName(ctx context.Context, name string) (*GroupEntry, error) {
	return s.group(ctx, "name = ?", name)
}

// GroupByGID implements Store.
func (s *SQLiteStore) GroupByGID(ctx context.Context, gid uint32) (*GroupEntry, error) {
	return s.group(ctx, "gid = ?", gid)
}

// group reads one group row.
func (s *SQLiteStore) group(ctx context.Context, where string, arg any) (*GroupEntry, error) {
	var row groupRow
	err := s.db.WithContext(ctx).Where(where, arg).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the group cache: %w", err)
	}
	e := GroupEntry(row)
	return &e, nil
}

// PutGroup implements Store.
func (s *SQLiteStore) PutGroup(ctx context.Context, e GroupEntry) error {
	row := groupRow(e)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("name = ? OR gid = ?", e.Name, e.GID).Delete(&groupRow{}).Error; err != nil {
			return err
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		return fmt.Errorf("write the group cache: %w", err)
	}
	return nil
}

// DeleteGroup implements Store.
func (s *SQLiteStore) DeleteGroup(ctx context.Context, name string) error {
	if err := s.db.WithContext(ctx).Where("name = ?", name).Delete(&groupRow{}).Error; err != nil {
		return fmt.Errorf("write the group cache: %w", err)
	}
	return nil
}

// MarkStale implements Store.
func (s *SQLiteStore) MarkStale(ctx context.Context, sel Selector) (int, error) {
	total := 0
	mark := func(model any, enabled bool) error {
		if !enabled {
			return nil
		}
		q := s.db.WithContext(ctx).Model(model)
		if sel.Name != "" {
			q = q.Where("name = ?", sel.Name)
		} else {
			q = q.Where("1 = 1")
		}
		res := q.Update("stale", true)
		total += int(res.RowsAffected)
		return res.Error
	}
	if err := errors.Join(mark(&userRow{}, sel.Users), mark(&groupRow{}, sel.Groups)); err != nil {
		return total, fmt.Errorf("mark the cache stale: %w", err)
	}
	return total, nil
}

// Users implements Store.
func (s *SQLiteStore) Users(ctx context.Context) ([]UserEntry, error) {
	var rows []userRow
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read the user cache: %w", err)
	}
	out := make([]UserEntry, len(rows))
	for i, r := range rows {
		out[i] = UserEntry(r)
	}
	return out, nil
}

// Groups implements Store.
func (s *SQLiteStore) Groups(ctx context.Context) ([]GroupEntry, error) {
	var rows []groupRow
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read the group cache: %w", err)
	}
	out := make([]GroupEntry, len(rows))
	for i, r := range rows {
		out[i] = GroupEntry(r)
	}
	return out, nil
}

// Counts implements Store.
func (s *SQLiteStore) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	var users, staleUsers, groups, staleGroups int64
	db := s.db.WithContext(ctx)
	err := errors.Join(
		db.Model(&userRow{}).Count(&users).Error,
		db.Model(&userRow{}).Where("stale = ?", true).Count(&staleUsers).Error,
		db.Model(&groupRow{}).Count(&groups).Error,
		db.Model(&groupRow{}).Where("stale = ?", true).Count(&staleGroups).Error,
	)
	if err != nil {
		return c, fmt.Errorf("count the cache: %w", err)
	}
	c.Users, c.StaleUsers, c.Groups, c.StaleGroups = int(users), int(staleUsers), int(groups), int(staleGroups)
	return c, nil
}

// Close implements Store.
func (s *SQLiteStore) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		// not covered: see OpenSQLite; the pool is always a *sql.DB.
		return fmt.Errorf("close the cache: %w", err)
	}
	return sqlDB.Close()
}
