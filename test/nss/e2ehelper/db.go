package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mnestor/ssoossh/server/dbtime"
	"github.com/mnestor/ssoossh/server/model"
)

// The fleet key shapes server/service/nss_keys.go uses: the id is "nss_"
// and 8 Crockford base32 symbols, lower-cased; the secret is
// "ssoossh_nss_" and 32 random bytes in unpadded base64url; the stored
// form is the hex SHA-256 of the secret.
const (
	keyIDPrefix     = "nss_"
	keySecretPrefix = "ssoossh_nss_"
	crockford       = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

// creatorID is the users row seed-key records as the key's creator, the
// stand-in for the admin who would have made it in the web UI.
const creatorID = "nss-e2e-key-admin"

// newKeyID draws a fleet key id.
func newKeyID() (string, error) {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return "", fmt.Errorf("generate a key id: %w", err)
	}
	var acc uint64
	for _, b := range raw {
		acc = acc<<8 | uint64(b)
	}
	var id strings.Builder
	id.WriteString(keyIDPrefix)
	for shift := 35; shift >= 0; shift -= 5 {
		id.WriteByte(crockford[(acc>>uint(shift))&31])
	}
	return strings.ToLower(id.String()), nil
}

// newKeySecret draws a fleet key secret.
func newKeySecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return "", fmt.Errorf("generate a key secret: %w", err)
	}
	return keySecretPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// hashSecret is the stored form of a secret.
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// openDB opens ssoosshd's sqlite database at path the way the server does
// (UTC timestamps through dbtime), waiting on the server's locks rather
// than failing on them.
func openDB(path string) (*gorm.DB, error) {
	if path == "" {
		return nil, errors.New("-db is required")
	}
	db, err := gorm.Open(sqlite.Open("file:"+filepath.Clean(path)+"?_pragma=busy_timeout(10000)"), &gorm.Config{
		TranslateError: true,
		NowFunc:        dbtime.NowFunc,
		Logger:         logger.Discard,
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.Use(dbtime.Plugin{}); err != nil {
		// not covered: the plugin registers once on a fresh handle.
		return nil, fmt.Errorf("register the UTC timestamp plugin: %w", err)
	}
	return db, nil
}

// runSeedKey writes a live fleet key, created by a seeded admin row, and
// the daemon's two-line key file (id, then secret) at -key-file, mode
// 0600. It prints the key id; the secret goes to the file only.
func runSeedKey(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("seed-key", flag.ContinueOnError)
	dbPath := fs.String("db", "", "ssoosshd's sqlite database")
	keyFile := fs.String("key-file", "nss.key", "where to write the daemon's key file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := openDB(*dbPath)
	if err != nil {
		return err
	}
	id, secret, err := seedKey(ctx, db, time.Now().UTC())
	if err != nil {
		return err
	}
	if err := os.WriteFile(*keyFile, []byte(id+"\n"+secret+"\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", *keyFile, err)
	}
	_, err = fmt.Fprintln(out, id)
	return err
}

// seedKey inserts the creator row when it is missing and a key row the
// way NSSKeyService.Create does, returning the key's id and secret.
func seedKey(ctx context.Context, db *gorm.DB, now time.Time) (id, secret string, err error) {
	if id, err = newKeyID(); err != nil {
		return "", "", err
	}
	if secret, err = newKeySecret(); err != nil {
		return "", "", err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		creator := model.User{ID: creatorID, Subject: "sub-" + creatorID, Username: creatorID, CreatedAt: now, UpdatedAt: now}
		if err := tx.Where(model.User{ID: creatorID}).FirstOrCreate(&creator).Error; err != nil {
			return fmt.Errorf("seed the key's creator: %w", err)
		}
		key := model.NSSKey{
			ID:                id,
			CurrentGeneration: 1,
			SecretHash:        hashSecret(secret),
			Name:              "nss e2e",
			Description:       "seeded by test/nss/e2ehelper",
			CreatedBy:         creatorID,
			ExpiresAt:         now.Add(24 * time.Hour),
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := tx.Create(&key).Error; err != nil {
			return fmt.Errorf("insert the fleet key: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return id, secret, nil
}

// runRevokeKey revokes the key -id, as NSSKeyService.Revoke records it.
func runRevokeKey(ctx context.Context, args []string, _ io.Writer) error {
	fs := flag.NewFlagSet("revoke-key", flag.ContinueOnError)
	dbPath := fs.String("db", "", "ssoosshd's sqlite database")
	id := fs.String("id", "", "the key id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := openDB(*dbPath)
	if err != nil {
		return err
	}
	return revokeKey(ctx, db, *id, time.Now().UTC())
}

// revokeKey sets the key's revoked_at and revoked_by, failing when no live
// key has that id.
func revokeKey(ctx context.Context, db *gorm.DB, id string, now time.Time) error {
	result := db.WithContext(ctx).Model(&model.NSSKey{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Updates(map[string]any{"revoked_at": now, "revoked_by": creatorID, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("revoke %s: %w", id, result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("revoke %s: no live key has that id", id)
	}
	return nil
}

// runLinked prints "linked" when the posix account -name is linked to a
// login (posix_users.user_id is set) and "unlinked" when it is not.
func runLinked(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("linked", flag.ContinueOnError)
	dbPath := fs.String("db", "", "ssoosshd's sqlite database")
	name := fs.String("name", "", "the account name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := openDB(*dbPath)
	if err != nil {
		return err
	}
	state, err := linkState(ctx, db, *name)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, state)
	return err
}

// linkState is "linked" or "unlinked" for the stored account name.
func linkState(ctx context.Context, db *gorm.DB, name string) (string, error) {
	var account model.PosixUser
	if err := db.WithContext(ctx).Where("name = ?", name).Take(&account).Error; err != nil {
		return "", fmt.Errorf("read the posix account %q: %w", name, err)
	}
	if account.UserID == nil {
		return "unlinked", nil
	}
	return "linked", nil
}
