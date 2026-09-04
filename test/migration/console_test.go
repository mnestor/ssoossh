package migration_test

// The console migration is the first in this repo to change a CHECK
// constraint, which on SQLite means rebuilding two tables with the
// documented 12-step procedure. The schema goldens next door pin what the
// rebuild lands on; this pins what it did to the rows and the constraints
// on the way — that data survived the copy, that the foreign key from
// certificates back into certificate_requests still holds, that the widened
// CHECK admits 'console' and still refuses anything else, and that the
// live-rows-only unique index on user_code means what it says.

import (
	"testing"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/sqlite"
)

// The migration under test, and the one immediately before it.
const (
	consoleLoginVersion   = 20260904000000
	beforeConsoleLoginVer = 20260829040000
)

// requestRow is the subset of certificate_requests these tests read. Not
// model.CertificateRequest: that carries the columns the migration is what
// adds, so GORM would try to select them before they exist.
type requestRow struct {
	ID       string
	Type     string
	Status   string
	Username string
	UserCode string
	Hostname string
}

// seedPreConsoleRows writes one user, one certificate request and one
// certificate through raw SQL, against the schema as it stood before the
// console migration. Raw SQL rather than the models for the same reason
// backfill_test.go uses it: the models describe the schema after.
func seedPreConsoleRows(t *testing.T, db *gorm.DB) {
	t.Helper()

	if err := db.Exec(`INSERT INTO users (id, subject, username, email, created_at, updated_at)
		VALUES ('u-1', 'sub-1', 'alice', 'alice@example.com', datetime('now'), datetime('now'))`).
		Error; err != nil {
		t.Fatalf("failed to seed the user: %v", err)
	}
	if err := db.Exec(`INSERT INTO certificate_requests
		(id, type, user_id, public_key, username, requested_options, source_ip, status, created_at, claim_user_agent)
		VALUES ('r-1', 'pam', 'u-1', 'ssh-ed25519 AAAA', 'alice', '{}', '198.51.100.7', 'approved', datetime('now'), 'curl/8')`).
		Error; err != nil {
		t.Fatalf("failed to seed the certificate request: %v", err)
	}
	if err := db.Exec(`INSERT INTO certificates
		(id, type, user_id, certificate_request_id, public_key_fingerprint, serial_number, key_id, issued_at, expires_at)
		VALUES ('c-1', 'pam', 'u-1', 'r-1', 'SHA256:abc', 42, 'pam:alice', datetime('now'), datetime('now', '+1 hour'))`).
		Error; err != nil {
		t.Fatalf("failed to seed the certificate: %v", err)
	}
}

// migratedWithSeededRows steps back to before the console migration, seeds
// rows against the old schema, and applies the migration.
func migratedWithSeededRows(t *testing.T) *gorm.DB {
	t.Helper()

	db := sqlite.ConnectAndMigrate(t)
	if err := sqlite.RunTo(t, db, beforeConsoleLoginVer); err != nil {
		t.Fatalf("failed to step back to %d: %v", beforeConsoleLoginVer, err)
	}
	seedPreConsoleRows(t, db)
	if err := sqlite.RunTo(t, db, consoleLoginVersion); err != nil {
		t.Fatalf("failed to apply %d: %v", consoleLoginVersion, err)
	}
	return db
}

// The rebuild copies every row into a new table and drops the old one, so
// the first thing to prove is that nothing was left behind.
func TestConsoleMigration_ShouldPreserveExistingRows(t *testing.T) {
	db := migratedWithSeededRows(t)

	var request requestRow
	if err := db.Raw(`SELECT id, type, status, username, user_code, hostname
		FROM certificate_requests WHERE id = 'r-1'`).Scan(&request).Error; err != nil {
		t.Fatalf("failed to read back the request: %v", err)
	}
	if request.ID != "r-1" || request.Type != "pam" || request.Status != "approved" || request.Username != "alice" {
		t.Errorf("request came back as %+v, want the pam/approved/alice row that went in", request)
	}
	// The new columns default to empty for every row that predates them.
	if request.UserCode != "" || request.Hostname != "" {
		t.Errorf("pre-existing row gained user_code=%q hostname=%q, want both empty", request.UserCode, request.Hostname)
	}

	var certCount int64
	if err := db.Raw(`SELECT count(*) FROM certificates WHERE id = 'c-1' AND certificate_request_id = 'r-1'`).
		Scan(&certCount).Error; err != nil {
		t.Fatalf("failed to count certificates: %v", err)
	}
	if certCount != 1 {
		t.Errorf("found %d certificates for c-1, want 1 — the rebuild lost the row or its request link", certCount)
	}
}

// certificates references certificate_requests, and the rebuild drops and
// recreates the table the reference points at. If the reference did not
// survive, an orphan insert would be accepted.
func TestConsoleMigration_ShouldKeepTheForeignKeyIntoRequests(t *testing.T) {
	db := migratedWithSeededRows(t)

	if err := db.Exec(`PRAGMA foreign_keys = ON`).Error; err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}
	err := db.Exec(`INSERT INTO certificates
		(id, type, user_id, certificate_request_id, public_key_fingerprint, serial_number, key_id, issued_at, expires_at)
		VALUES ('c-orphan', 'user', 'u-1', 'no-such-request', 'SHA256:def', 43, 'alice', datetime('now'), datetime('now', '+1 hour'))`).
		Error
	if err == nil {
		t.Fatal("a certificate referencing a non-existent request was accepted; the foreign key did not survive the rebuild")
	}
}

func TestConsoleMigration_ShouldAdmitConsoleAndStillRefuseUnknownTypes(t *testing.T) {
	tests := []struct {
		name     string
		certType string
		wantErr  bool
	}{
		{name: "console is now allowed", certType: "console"},
		{name: "user still is", certType: "user"},
		{name: "service still is", certType: "service"},
		{name: "pam still is", certType: "pam"},
		{name: "host is still refused", certType: "host", wantErr: true},
		{name: "a typo is still refused", certType: "consoel", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := migratedWithSeededRows(t)

			err := db.Exec(`INSERT INTO certificate_requests
				(id, type, public_key, username, requested_options, source_ip, status, created_at, claim_user_agent, user_code, hostname, pam_service, tty, remote_host)
				VALUES ('r-typed', ?, 'ssh-ed25519 AAAA', 'alice', '{}', '10.20.3.4', 'pending', datetime('now'), '', '', 'web01', 'login', 'tty1', '')`,
				tt.certType).Error

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

// A console certificate has to be insertable too — the certificates table
// carries its own type CHECK, and forgetting to widen it would only surface
// when the signer wrote the row.
func TestConsoleMigration_ShouldAdmitAConsoleCertificate(t *testing.T) {
	db := migratedWithSeededRows(t)

	if err := db.Exec(`INSERT INTO certificates
		(id, type, user_id, certificate_request_id, public_key_fingerprint, serial_number, key_id, issued_at, expires_at)
		VALUES ('c-console', 'console', 'u-1', 'r-1', 'SHA256:ghi', 44, 'console:alice', datetime('now'), datetime('now', '+1 hour'))`).
		Error; err != nil {
		t.Fatalf("a console certificate was refused: %v", err)
	}
}

// Uniqueness is over live rows only. Both halves of that matter: a second
// live request cannot take a code that is in use, and a resolved one
// releases it.
func TestConsoleMigration_ShouldEnforceUserCodeUniquenessOverLiveRowsOnly(t *testing.T) {
	insert := func(t *testing.T, db *gorm.DB, id, status, code string) error {
		t.Helper()
		return db.Exec(`INSERT INTO certificate_requests
			(id, type, public_key, username, requested_options, source_ip, status, created_at, claim_user_agent, user_code, hostname, pam_service, tty, remote_host)
			VALUES (?, 'console', 'ssh-ed25519 AAAA', 'alice', '{}', '10.20.3.4', ?, datetime('now'), '', ?, 'web01', 'login', 'tty1', '')`,
			id, status, code).Error
	}

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
			db := migratedWithSeededRows(t)

			if err := insert(t, db, "r-a", tt.firstStatus, "K7M4QP2X"); err != nil {
				t.Fatalf("the first insert failed: %v", err)
			}
			err := insert(t, db, "r-b", tt.secondStatus, "K7M4QP2X")

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
// tolerate any number of them — which is what the `user_code <> ”`
// predicate is for.
func TestConsoleMigration_ShouldAllowManyRequestsWithNoUserCode(t *testing.T) {
	db := migratedWithSeededRows(t)

	for _, id := range []string{"r-x", "r-y", "r-z"} {
		if err := db.Exec(`INSERT INTO certificate_requests
			(id, type, public_key, username, requested_options, source_ip, status, created_at, claim_user_agent, user_code, hostname, pam_service, tty, remote_host)
			VALUES (?, 'user', 'ssh-ed25519 AAAA', 'alice', '{}', '10.20.3.4', 'pending', datetime('now'), '', '', '', '', '', '')`,
			id).Error; err != nil {
			t.Fatalf("insert of %s was refused: %v", id, err)
		}
	}
}

// The down migration drops console rows rather than relabelling them, so a
// rollback leaves a schema the previous release can read.
func TestConsoleMigration_ShouldDropConsoleRowsOnTheWayDown(t *testing.T) {
	db := migratedWithSeededRows(t)

	if err := db.Exec(`INSERT INTO certificate_requests
		(id, type, public_key, username, requested_options, source_ip, status, created_at, claim_user_agent, user_code, hostname, pam_service, tty, remote_host)
		VALUES ('r-console', 'console', 'ssh-ed25519 AAAA', 'alice', '{}', '10.20.3.4', 'approved', datetime('now'), '', 'K7M4QP2X', 'web01', 'login', 'tty1', '')`).
		Error; err != nil {
		t.Fatalf("failed to seed the console request: %v", err)
	}
	if err := db.Exec(`INSERT INTO certificates
		(id, type, user_id, certificate_request_id, public_key_fingerprint, serial_number, key_id, issued_at, expires_at)
		VALUES ('c-console', 'console', 'u-1', 'r-console', 'SHA256:ghi', 44, 'console:alice', datetime('now'), datetime('now', '+1 hour'))`).
		Error; err != nil {
		t.Fatalf("failed to seed the console certificate: %v", err)
	}

	if err := sqlite.RunTo(t, db, beforeConsoleLoginVer); err != nil {
		t.Fatalf("failed to step back to %d: %v", beforeConsoleLoginVer, err)
	}

	var consoleRows int64
	if err := db.Raw(`SELECT count(*) FROM certificate_requests WHERE type = 'console'`).
		Scan(&consoleRows).Error; err != nil {
		t.Fatalf("failed to count console requests: %v", err)
	}
	if consoleRows != 0 {
		t.Errorf("%d console requests survived the downgrade, want none", consoleRows)
	}

	// The rows that predate the console type have to come back through the
	// downgrade's own rebuild intact.
	var survivor requestRow
	if err := db.Raw(`SELECT id, type, status, username FROM certificate_requests WHERE id = 'r-1'`).
		Scan(&survivor).Error; err != nil {
		t.Fatalf("failed to read back the pre-existing request: %v", err)
	}
	if survivor.ID != "r-1" || survivor.Type != "pam" {
		t.Errorf("pre-existing request came back as %+v, want the pam row", survivor)
	}
}
