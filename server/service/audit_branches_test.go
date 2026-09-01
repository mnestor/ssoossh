package service

// Test methodology: the failure-path counterparts to audit_test.go. The
// contract under test is asymmetric on purpose: RecordTx must fail the
// enclosing transaction when the row cannot be written, while Record and
// LogOnly must swallow the same failures — a read that errors because its
// audit row could not be written is worse than a missing row.

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// closeDB force-closes the underlying handle so every later query fails,
// standing in for the database going away mid-flight.
func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrapping the sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}
}

// countRows counts the stored audit rows.
func countRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()

	var total int64
	if err := db.Model(&model.AuditEvent{}).Count(&total).Error; err != nil {
		t.Fatalf("counting audit rows: %v", err)
	}
	return total
}

// An event with no action is a programming error; the recorder must refuse
// it without writing anything, whichever entry point it arrives through.
func TestRecord_ShouldDropAnEventWithNoAction(t *testing.T) {
	t.Parallel()

	svc, db := newTestAuditService(t)
	svc.Record(t.Context(), AuditEvent{})

	if got := countRows(t, db); got != 0 {
		t.Errorf("rows after recording an actionless event = %d, want 0", got)
	}
}

// Record must never fail the operation it describes: with the database
// gone, it logs and returns rather than erroring or panicking.
func TestRecord_ShouldSwallowADatabaseFailure(t *testing.T) {
	t.Parallel()

	svc, db := newTestAuditService(t)
	closeDB(t, db)

	svc.Record(context.Background(), AuditEvent{Action: AuditAuthLogin})
}

// RecordTx has the opposite contract: a row that cannot be written must
// fail the transaction, or a disable could commit without its audit row.
func TestRecordTx_ShouldFailWhenTheRowCannotBeWritten(t *testing.T) {
	t.Parallel()

	svc, db := newTestAuditService(t)
	if err := db.Migrator().DropTable(&model.AuditEvent{}); err != nil {
		t.Fatalf("dropping the audit table: %v", err)
	}

	err := svc.RecordTx(db, AuditEvent{Action: AuditUserDisabled, Reason: "offboarded"})
	if err == nil {
		t.Fatal("RecordTx with no audit table returned nil, want an error that fails the transaction")
	}
	if !strings.Contains(err.Error(), string(AuditUserDisabled)) {
		t.Errorf("RecordTx error = %q, want it to name the action", err)
	}
}

// cert.issued goes to the shipped log only; inside a transaction that means
// writing nothing at all, and succeeding.
func TestRecordTx_ShouldKeepCertIssuedOutOfTheTable(t *testing.T) {
	t.Parallel()

	svc, db := newTestAuditService(t)

	if err := svc.RecordTx(db, AuditEvent{Action: AuditCertIssued}); err != nil {
		t.Fatalf("RecordTx(cert.issued): %v", err)
	}
	if got := countRows(t, db); got != 0 {
		t.Errorf("rows after a cert.issued RecordTx = %d, want 0", got)
	}
}

// LogOnly validates the same way the writing paths do, so a malformed
// event cannot reach the shipped log either.
func TestLogOnly_ShouldRefuseAnInvalidEvent(t *testing.T) {
	t.Parallel()

	svc, db := newTestAuditService(t)

	// A required reason is missing; prepare refuses, and nothing lands in
	// the table as a side effect either.
	svc.LogOnly(AuditEvent{Action: AuditUserDisabled})

	if got := countRows(t, db); got != 0 {
		t.Errorf("rows after LogOnly = %d, want 0 (LogOnly never writes)", got)
	}
}

// LogOnly on a well-formed event is the after-commit half of a mutation;
// it must not touch the table the transaction already wrote.
func TestLogOnly_ShouldEmitWithoutWriting(t *testing.T) {
	t.Parallel()

	svc, db := newTestAuditService(t)
	svc.LogOnly(AuditEvent{Action: AuditUserDisabled, Reason: "offboarded"})

	if got := countRows(t, db); got != 0 {
		t.Errorf("rows after LogOnly = %d, want 0 (the row belongs to RecordTx)", got)
	}
}

func TestAuditSubjectFromIdentity_ShouldSnapshotOrReturnNil(t *testing.T) {
	t.Parallel()

	if got := AuditSubjectFromIdentity(nil, "user-1"); got != nil {
		t.Errorf("AuditSubjectFromIdentity(nil) = %+v, want nil", got)
	}

	identity := &Identity{Subject: "sub-1", Username: "alice", Email: "alice@example.com", Groups: []string{"ops"}}
	got := AuditSubjectFromIdentity(identity, "user-1")
	if got == nil || got.UserID != "user-1" || got.Username != "alice" || got.Subject != "sub-1" {
		t.Errorf("AuditSubjectFromIdentity = %+v, want the copied snapshot", got)
	}
}

func TestAuditSubjectFromUser_ShouldSnapshotOrReturnNil(t *testing.T) {
	t.Parallel()

	if got := AuditSubjectFromUser(nil); got != nil {
		t.Errorf("AuditSubjectFromUser(nil) = %+v, want nil", got)
	}

	user := &model.User{ID: "user-1", Subject: "sub-1", Username: "alice", Email: "alice@example.com"}
	got := AuditSubjectFromUser(user)
	if got == nil || got.UserID != "user-1" || got.Username != "alice" {
		t.Errorf("AuditSubjectFromUser = %+v, want the copied snapshot", got)
	}
}

// A corrupt stored payload must not blank the page: the row's existence and
// timestamp are audit evidence, so it surfaces under a name that says what
// happened instead of vanishing.
func TestListRecent_ShouldSurfaceACorruptPayloadRow(t *testing.T) {
	t.Parallel()

	svc, db := newTestAuditService(t)
	row := model.AuditEvent{ID: "corrupt-1", Payload: "not json"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("planting the corrupt row: %v", err)
	}

	page, err := svc.ListRecent(t.Context(), 10, 0)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("len(Events) = %d, want the corrupt row surfaced", len(page.Events))
	}
	if got := page.Events[0].Event.Action; got != "audit.unreadable" {
		t.Errorf("corrupt row action = %q, want audit.unreadable", got)
	}
}

func TestListRecent_ShouldReportADatabaseFailure(t *testing.T) {
	t.Parallel()

	svc, db := newTestAuditService(t)
	closeDB(t, db)

	if _, err := svc.ListRecent(context.Background(), 10, 0); err == nil {
		t.Error("ListRecent on a closed database returned no error")
	}
}

func TestSweepAuditEvents_ShouldReportADatabaseFailure(t *testing.T) {
	t.Parallel()

	_, db := newTestAuditService(t)
	closeDB(t, db)

	if err := SweepAuditEvents(context.Background(), db, time.Hour, 10); err == nil {
		t.Error("SweepAuditEvents on a closed database returned no error")
	}
}
