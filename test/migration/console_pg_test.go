//go:build dbparity

package migration_test

// The SQLite side of the console migration is pinned by console_test.go.
// This is the Postgres half the design asked for
// (docs/proposals/console-login-pam.md, "Certificate type"): the widened
// CHECK on both tables admits a console row and still refuses anything
// else. The two dialects change the constraint by different means (a
// rebuild on SQLite, DROP/ADD CONSTRAINT on Postgres), so a green SQLite
// run says nothing about this one.

import (
	"testing"

	"github.com/mnestor/ssoossh/test/postgres"
)

func TestConsoleMigration_PostgresShouldAdmitConsoleAndStillRefuseUnknownTypes(t *testing.T) {
	// Not t.Parallel() — see TestMigrationParity_SchemasShouldBeIdentical.
	ctx := t.Context()

	db, _ := postgres.ConnectAndMigrate(t, ctx)

	if err := db.Exec(`INSERT INTO users (id, subject, username, email, created_at, updated_at)
		VALUES ('u-1', 'sub-1', 'alice', 'alice@example.com', now(), now())`).Error; err != nil {
		t.Fatalf("failed to seed the user: %v", err)
	}

	tests := []struct {
		name     string
		certType string
		wantErr  bool
	}{
		{name: "console is allowed", certType: "console"},
		{name: "user still is", certType: "user"},
		{name: "service still is", certType: "service"},
		{name: "pam still is", certType: "pam"},
		{name: "host is refused", certType: "host", wantErr: true},
		{name: "a typo is refused", certType: "consoel", wantErr: true},
	}

	for i, tt := range tests {
		t.Run("request: "+tt.name, func(t *testing.T) {
			err := db.Exec(`INSERT INTO certificate_requests
				(id, type, user_id, public_key, username, requested_options, source_ip, status, created_at, claim_user_agent, user_code, hostname, pam_service, tty, remote_host)
				VALUES (?, ?, 'u-1', 'ssh-ed25519 AAAA', 'alice', '{}', '10.20.3.4', 'approved', now(), '', '', 'web01', 'login', 'tty1', '')`,
				requestID(i), tt.certType).Error

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

	// The certificates table carries its own CHECK; forgetting to widen it
	// would only surface when the signer wrote the row.
	for i, tt := range tests {
		if tt.wantErr {
			continue
		}
		t.Run("certificate: "+tt.name, func(t *testing.T) {
			err := db.Exec(`INSERT INTO certificates
				(id, type, user_id, certificate_request_id, public_key_fingerprint, serial_number, key_id, issued_at, expires_at)
				VALUES (?, ?, 'u-1', ?, ?, ?, ?, now(), now() + interval '1 hour')`,
				"c-"+tt.certType, tt.certType, requestID(i), "SHA256:"+tt.certType, 100+i, tt.certType+":alice").Error
			if err != nil {
				t.Fatalf("a %q certificate was refused: %v", tt.certType, err)
			}
		})
	}

	if err := db.Exec(`INSERT INTO certificates
		(id, type, user_id, certificate_request_id, public_key_fingerprint, serial_number, key_id, issued_at, expires_at)
		VALUES ('c-host', 'host', 'u-1', ?, 'SHA256:host', 999, 'host:web01', now(), now() + interval '1 hour')`,
		requestID(0)).Error; err == nil {
		t.Fatal("a 'host' certificate was accepted, want the CHECK to refuse it")
	}
}

// requestID names the request row seeded for the i-th type case, so the
// certificate insert for that type can point at a row of the same type.
func requestID(i int) string {
	return "r-" + string(rune('a'+i))
}
