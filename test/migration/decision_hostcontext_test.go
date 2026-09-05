package migration_test

// The decision host-context migration copies what asked for a certificate
// off certificate_requests and onto certificate_request_decisions, where it
// can outlive the request. Without the backfill the certificate history
// would show that context only for decisions made after the release and be
// blank for the whole history that already has the data one join away, so
// what the backfill does to existing rows is the behavior worth pinning.

import (
	"testing"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/sqlite"
)

// The migration under test, and the one immediately before it.
const (
	decisionHostContextVersion = 20260905010000
	beforeDecisionHostContext  = 20260905000000
)

// decisionRow is the subset this test reads. Not
// model.CertificateRequestDecision: that carries the columns the migration
// is what adds, so GORM would try to read them before they exist.
type decisionRow struct {
	ReportedUsername string
	ReportedHostname string
	PAMService       string
	Process          string
	Client           string
}

// seedRequest writes one certificate_requests row through raw SQL, for the
// same reason the enrollment backfill test does: the model has moved on
// from the schema at this point in the migration history.
func seedRequest(t *testing.T, db *gorm.DB, id, certType, username, hostname, localUsername, localHostname string) {
	t.Helper()

	if err := db.Exec(`INSERT INTO certificate_requests
		(id, type, status, public_key, username, hostname, local_username, local_hostname,
		 pam_service, tty, remote_host, requesting_user, process, machine_id, client,
		 created_at)
		VALUES (?, ?, 'approved', 'ssh-ed25519 AAAA...', ?, ?, ?, ?,
		 'sudo', 'pts/3', '', 'alice', 'sudo -i', '3f2c1e0d9b8a7f6e', 'pam_ssoossh-c/0.3.0',
		 datetime('now'))`,
		id, certType, username, hostname, localUsername, localHostname).Error; err != nil {
		t.Fatalf("failed to seed the request: %v", err)
	}
}

// seedDecision writes one decision against requestID.
func seedDecision(t *testing.T, db *gorm.DB, id, requestID string) {
	t.Helper()

	if err := db.Exec(`INSERT INTO certificate_request_decisions
		(id, certificate_request_id, outcome, subject, username, email, groups,
		 other_accounts, service_accounts, source_ip, user_agent, accept_language,
		 forwarded_for, policy_explanation, principals, granted_options, decided_at)
		VALUES (?, ?, 'approved', 'sub-approver', 'mike.nestor', 'mike@example.org', '[]',
		 '[]', '[]', '203.0.113.9', 'Mozilla/5.0', 'en-US', '', '', '[]', '{}', datetime('now'))`,
		id, requestID).Error; err != nil {
		t.Fatalf("failed to seed the decision: %v", err)
	}
}

// readDecision reads the snapshot columns back after the migration.
func readDecision(t *testing.T, db *gorm.DB, id string) decisionRow {
	t.Helper()

	var row decisionRow
	if err := db.Raw(`SELECT reported_username, reported_hostname, pam_service, process, client
		FROM certificate_request_decisions WHERE id = ?`, id).Scan(&row).Error; err != nil {
		t.Fatalf("failed to read back the decision: %v", err)
	}
	return row
}

// steppedBack returns a database at the migration immediately before the
// one under test, ready to be seeded.
func steppedBack(t *testing.T) *gorm.DB {
	t.Helper()

	db := sqlite.ConnectAndMigrate(t)
	if err := sqlite.RunTo(t, db, beforeDecisionHostContext); err != nil {
		t.Fatalf("failed to step back to %d: %v", beforeDecisionHostContext, err)
	}
	return db
}

func TestDecisionHostContextMigration_ShouldBackfillFromTheRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		certType     string
		username     string
		hostname     string
		local        string
		localHost    string
		wantUsername string
		wantHostname string
	}{
		{
			name:     "should take the PAM columns when the request is a PAM one",
			certType: "pam", username: "root", hostname: "web01",
			local: "ignored", localHost: "ignored",
			wantUsername: "root", wantHostname: "web01",
		},
		{
			name:     "should take the PAM columns when the request is a console one",
			certType: "console", username: "operator", hostname: "rack07",
			wantUsername: "operator", wantHostname: "rack07",
		},
		// The case the certificate history lost: a user certificate carries
		// its user@host in local_username/local_hostname, and reading the
		// PAM pair for it reports nobody.
		{
			name:     "should take the local columns when the request is a user one",
			certType: "user", local: "alice", localHost: "alice-laptop",
			wantUsername: "alice", wantHostname: "alice-laptop",
		},
		{
			name:     "should take the local columns when the request is a service one",
			certType: "service", local: "ci", localHost: "runner-3",
			wantUsername: "ci", wantHostname: "runner-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := steppedBack(t)
			seedRequest(t, db, "req-1", tt.certType, tt.username, tt.hostname, tt.local, tt.localHost)
			seedDecision(t, db, "dec-1", "req-1")

			if err := sqlite.RunTo(t, db, decisionHostContextVersion); err != nil {
				t.Fatalf("failed to apply %d: %v", decisionHostContextVersion, err)
			}

			got := readDecision(t, db, "dec-1")
			if got.ReportedUsername != tt.wantUsername || got.ReportedHostname != tt.wantHostname {
				t.Errorf("reported identity = %q@%q, want %q@%q",
					got.ReportedUsername, got.ReportedHostname, tt.wantUsername, tt.wantHostname)
			}
		})
	}
}

func TestDecisionHostContextMigration_ShouldBackfillTheRestOfTheContext(t *testing.T) {
	t.Parallel()

	db := steppedBack(t)
	seedRequest(t, db, "req-2", "pam", "root", "web01", "", "")
	seedDecision(t, db, "dec-2", "req-2")

	if err := sqlite.RunTo(t, db, decisionHostContextVersion); err != nil {
		t.Fatalf("failed to apply %d: %v", decisionHostContextVersion, err)
	}

	got := readDecision(t, db, "dec-2")
	for name, pair := range map[string][2]string{
		"pam_service": {got.PAMService, "sudo"},
		"process":     {got.Process, "sudo -i"},
		"client":      {got.Client, "pam_ssoossh-c/0.3.0"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %q, want %q", name, pair[0], pair[1])
		}
	}
}

// certificate_request_decisions keeps no foreign key into
// certificate_requests, precisely so requests can be pruned. A decision
// whose request is already gone must migrate to the empty defaults rather
// than failing the migration for everyone.
func TestDecisionHostContextMigration_ShouldLeaveADecisionWithNoRequestEmpty(t *testing.T) {
	t.Parallel()

	db := steppedBack(t)
	seedDecision(t, db, "dec-orphan", "req-that-was-pruned")

	if err := sqlite.RunTo(t, db, decisionHostContextVersion); err != nil {
		t.Fatalf("failed to apply %d: %v", decisionHostContextVersion, err)
	}

	got := readDecision(t, db, "dec-orphan")
	if got.ReportedUsername != "" || got.ReportedHostname != "" || got.PAMService != "" {
		t.Errorf("orphaned decision = %+v, want the empty defaults", got)
	}
}
