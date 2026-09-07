package migration_test

// The schema goldens next door pin the shape the migration builds: which
// columns, which types, which constraints exist. This pins what three of
// those constraints actually do to rows, which a golden cannot say:
//
//   * the certificate-type CHECK on both certificate_requests and
//     certificates admits the four types and refuses everything else,
//     'host' included
//     (https://mnestor.github.io/ssoossh/project/decisions/);
//   * the foreign key from certificates back into certificate_requests
//     refuses an orphan;
//   * the unique index on user_code is over live rows only, so a code is
//     unique among requests that can still be approved and released once
//     one is resolved.
//
// The Postgres half of the same three properties is constraints_pg_test.go.

import (
	"testing"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/sqlite"
)

// seedUser writes the one user row the foreign keys below need. Raw SQL
// rather than the model, matching the rest of this package: these tests
// describe the migration's schema, not GORM's view of it.
func seedUser(t *testing.T, db *gorm.DB) {
	t.Helper()

	if err := db.Exec(`INSERT INTO users (id, subject, username, email, created_at, updated_at)
		VALUES ('u-1', 'sub-1', 'alice', 'alice@example.com', datetime('now'), datetime('now'))`).
		Error; err != nil {
		t.Fatalf("failed to seed the user: %v", err)
	}
}

// migratedWithUser returns a fully migrated database holding that user.
func migratedWithUser(t *testing.T) *gorm.DB {
	t.Helper()

	db := sqlite.ConnectAndMigrate(t)
	seedUser(t, db)
	return db
}

// insertRequest writes a certificate_requests row of the given type and
// status, returning whatever the database made of it.
func insertRequest(t *testing.T, db *gorm.DB, id, certType, status, userCode string) error {
	t.Helper()

	return db.Exec(`INSERT INTO certificate_requests
		(id, type, public_key, username, requested_options, source_ip, status, created_at, user_code, hostname, pam_service, tty)
		VALUES (?, ?, 'ssh-ed25519 AAAA', 'alice', '{}', '10.20.3.4', ?, datetime('now'), ?, 'web01', 'login', 'tty1')`,
		id, certType, status, userCode).Error
}

func TestCertificateTypeCheck_ShouldAdmitTheFourTypesAndRefuseTheRest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		certType string
		wantErr  bool
	}{
		{name: "user is allowed", certType: "user"},
		{name: "service is allowed", certType: "service"},
		{name: "pam is allowed", certType: "pam"},
		{name: "console is allowed", certType: "console"},
		{name: "host is refused", certType: "host", wantErr: true},
		{name: "a typo is refused", certType: "consoel", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := migratedWithUser(t)
			err := insertRequest(t, db, "r-typed", tt.certType, "pending", "")

			if tt.wantErr {
				if err == nil {
					t.Fatalf("type %q was accepted, want the CHECK to refuse it", tt.certType)
				}
				return
			}
			if err != nil {
				t.Fatalf("type %q was refused: %v", tt.certType, err)
			}
		})
	}
}

// The certificates table carries its own type CHECK; a type admitted by the
// request table and refused here would only surface when the signer wrote
// the row.
func TestCertificateTypeCheck_ShouldApplyToCertificatesToo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		certType string
		wantErr  bool
	}{
		{name: "user is allowed", certType: "user"},
		{name: "service is allowed", certType: "service"},
		{name: "pam is allowed", certType: "pam"},
		{name: "console is allowed", certType: "console"},
		{name: "host is refused", certType: "host", wantErr: true},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := migratedWithUser(t)
			// A request of a refused type cannot be seeded, so every case
			// hangs its certificate off one request of an allowed type.
			if err := insertRequest(t, db, "r-1", "user", "approved", ""); err != nil {
				t.Fatalf("failed to seed the request: %v", err)
			}

			err := db.Exec(`INSERT INTO certificates
				(id, type, user_id, certificate_request_id, public_key_fingerprint, serial_number, key_id, issued_at, expires_at)
				VALUES ('c-1', ?, 'u-1', 'r-1', ?, ?, ?, datetime('now'), datetime('now', '+1 hour'))`,
				tt.certType, "SHA256:"+tt.certType, 100+i, tt.certType+":alice").Error

			if tt.wantErr {
				if err == nil {
					t.Fatalf("a %q certificate was accepted, want the CHECK to refuse it", tt.certType)
				}
				return
			}
			if err != nil {
				t.Fatalf("a %q certificate was refused: %v", tt.certType, err)
			}
		})
	}
}

// certificates references certificate_requests, which is what keeps the
// audit chain request -> decision -> certificate joinable. SQLite enforces
// foreign keys only when asked, so the pragma is part of the test.
func TestCertificates_ShouldRefuseARequestThatDoesNotExist(t *testing.T) {
	t.Parallel()

	db := migratedWithUser(t)
	if err := db.Exec(`PRAGMA foreign_keys = ON`).Error; err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}

	err := db.Exec(`INSERT INTO certificates
		(id, type, user_id, certificate_request_id, public_key_fingerprint, serial_number, key_id, issued_at, expires_at)
		VALUES ('c-orphan', 'user', 'u-1', 'no-such-request', 'SHA256:def', 43, 'alice', datetime('now'), datetime('now', '+1 hour'))`).
		Error
	if err == nil {
		t.Fatal("a certificate referencing a non-existent request was accepted; the foreign key is not there")
	}
}

// Uniqueness is over live rows only. Both halves of that matter: a second
// live request cannot take a code that is in use, and a resolved one
// releases it.
func TestUserCodeIndex_ShouldEnforceUniquenessOverLiveRowsOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		firstStatus   string
		secondStatus  string
		wantSecondErr bool
	}{
		{name: "two pending requests cannot share a code", firstStatus: "pending", secondStatus: "pending", wantSecondErr: true},
		{name: "a pending and a signing request cannot share one", firstStatus: "signing", secondStatus: "pending", wantSecondErr: true},
		{name: "a denied request releases its code", firstStatus: "denied", secondStatus: "pending"},
		{name: "an expired request releases its code", firstStatus: "expired", secondStatus: "pending"},
		{name: "two resolved requests may share one", firstStatus: "approved", secondStatus: "denied"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := migratedWithUser(t)
			if err := insertRequest(t, db, "r-a", "console", tt.firstStatus, "K7M4QP2X"); err != nil {
				t.Fatalf("the first insert failed: %v", err)
			}
			err := insertRequest(t, db, "r-b", "console", tt.secondStatus, "K7M4QP2X")

			if tt.wantSecondErr {
				if err == nil {
					t.Fatal("the second request took a code already in use by a live one")
				}
				return
			}
			if err != nil {
				t.Fatalf("the second insert was refused: %v", err)
			}
		})
	}
}

// Every non-console request stores an empty code, so the index has to
// tolerate any number of them — which is what the index's
// non-empty-user_code predicate is for.
func TestUserCodeIndex_ShouldAllowManyRequestsWithNoUserCode(t *testing.T) {
	t.Parallel()

	db := migratedWithUser(t)

	for _, id := range []string{"r-x", "r-y", "r-z"} {
		if err := insertRequest(t, db, id, "user", "pending", ""); err != nil {
			t.Fatalf("insert of %s was refused: %v", id, err)
		}
	}
}
