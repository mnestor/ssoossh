package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Every cert.* event carries the account and host a PAM or console request
// claimed for itself. A reviewer reading an approval, a denial or an issued
// certificate should not have to join back to cert.requested to learn which
// account on which machine it was about, and the issued line is the one
// joined against that machine's own sshd and sudo logs.

// pamAuditOptions is the PAM policy the approval tests below need: a
// required group the approver is in, and a lifetime.
func pamAuditOptions() config.CertificateOptions {
	return config.CertificateOptions{
		PAM: config.CertOptionsPAM{Require: &config.PolicyCondition{Group: "sudoers"}, ValidDuration: 30 * time.Second},
	}
}

// pamApprover holds the account the request names, so approval succeeds
// with no explicit selection.
func pamApprover() *Identity {
	return &Identity{
		Username:      "mike.nestor",
		OtherAccounts: []string{"mnestor"},
		Subject:       "sub-approver",
		Groups:        []string{"sudoers"},
	}
}

// withAuditTable attaches a real recorder over the service's own database,
// with the audit table migrated, and returns a buffer that receives the
// shipped-log lines so a table-skipped event can be read back too.
func withAuditTable(t *testing.T, svc *CertRequestService) *bytes.Buffer {
	t.Helper()

	if err := svc.db.AutoMigrate(&model.AuditEvent{}); err != nil {
		t.Fatalf("failed to migrate the audit table: %v", err)
	}
	var shipped bytes.Buffer
	svc.SetAuditor(&AuditService{
		db:     svc.db,
		config: &config.Config{},
		log:    slog.New(slog.NewJSONHandler(&shipped, nil)),
	})
	return &shipped
}

// pamRequest creates a PAM request claiming account mnestor on host web01.
func pamRequest(t *testing.T, svc *CertRequestService) string {
	t.Helper()

	requestID, err := svc.createRequestID(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypePAM,
		PublicKey: "ssh-ed25519 AAAA...",
		Username:  "mnestor",
		Hostname:  "web01",
	})
	if err != nil {
		t.Fatalf("unexpected error creating the PAM request: %v", err)
	}
	return requestID
}

// storedDetail reads back the detail map of the one stored event with the
// given action, failing if there is not exactly one. The action lives in
// the payload, not a column, so the rows are decoded and filtered here.
func storedDetail(t *testing.T, svc *CertRequestService, action AuditAction) map[string]any {
	t.Helper()

	var rows []model.AuditEvent
	if err := svc.db.Find(&rows).Error; err != nil {
		t.Fatalf("failed to load audit events: %v", err)
	}
	var matched []AuditEvent
	for _, row := range rows {
		if event := decodePayload(t, row); event.Action == action {
			matched = append(matched, event)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("got %d %s events, want exactly one", len(matched), action)
	}
	return matched[0].Detail
}

// assertClaimedContext checks the two claimed fields on a detail map.
func assertClaimedContext(t *testing.T, action AuditAction, detail map[string]any, wantUsername, wantHostname string) {
	t.Helper()

	tests := []struct {
		key  string
		want string
	}{
		{key: "username", want: wantUsername},
		{key: "hostname", want: wantHostname},
	}
	for _, tt := range tests {
		got, ok := detail[tt.key]
		if !ok {
			t.Errorf("%s: detail has no %q key", action, tt.key)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: detail[%q] = %v, want %q", action, tt.key, got, tt.want)
		}
	}
}

func TestAudit_ShouldRecordTheClaimedAccountAndHostWhenAPAMRequestIsCreated(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)

	pamRequest(t, svc)

	assertClaimedContext(t, AuditCertRequested, storedDetail(t, svc, AuditCertRequested), "mnestor", "web01")
}

func TestAudit_ShouldRecordTheClaimedAccountAndHostWhenAPAMRequestIsApproved(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)
	requestID := pamRequest(t, svc)

	identity := pamApprover()
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}, ApprovalSelection{}); err != nil {
		t.Fatalf("unexpected error approving the request: %v", err)
	}

	assertClaimedContext(t, AuditCertApproved, storedDetail(t, svc, AuditCertApproved), "mnestor", "web01")
}

func TestAudit_ShouldRecordTheClaimedAccountAndHostWhenAPAMRequestIsDenied(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)
	requestID := pamRequest(t, svc)

	if err := svc.Deny(context.Background(), requestID, pamApprover(), DecisionContext{}); err != nil {
		t.Fatalf("unexpected error denying the request: %v", err)
	}

	detail := storedDetail(t, svc, AuditCertDenied)
	assertClaimedContext(t, AuditCertDenied, detail, "mnestor", "web01")
	if got := detail["cert_type"]; got != string(model.CertificateTypePAM) {
		t.Errorf("cert.denied: detail[\"cert_type\"] = %v, want %q", got, model.CertificateTypePAM)
	}
}

func TestAudit_ShouldRecordTheClaimedAccountAndHostWhenAConsoleCodeIsResolved(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute))
	withAuditTable(t, svc)
	seedUser(t, svc.db, "sub-approver")
	created := newConsoleRequest(t, svc, NewCertRequestParams{Username: "alice", Hostname: "console01"})

	if _, err := svc.ResolveUserCode(context.Background(), FormatUserCode(created.UserCode), &Identity{
		Subject:  "sub-approver",
		Username: "approver",
	}); err != nil {
		t.Fatalf("unexpected error resolving the code: %v", err)
	}

	assertClaimedContext(t, AuditCertCodeResolved, storedDetail(t, svc, AuditCertCodeResolved), "alice", "console01")
}

// cert.issued never reaches the table, so it is read back from the shipped
// log the recorder writes instead.
func TestAudit_ShouldRecordTheClaimedAccountAndHostWhenAPAMCertificateIsIssued(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	shipped := withAuditTable(t, svc)
	h := newTestSignedReplyHandler(t, svc)
	requestID := pamRequest(t, svc)
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusSigning).Error; err != nil {
		t.Fatalf("failed to move the request to signing: %v", err)
	}
	shipped.Reset()

	if _, err := h.recordCertificate(context.Background(), certmsg.SignedReply{
		RequestID:            requestID,
		Type:                 model.CertificateTypePAM,
		PublicKeyFingerprint: "SHA256:test",
		Serial:               42,
		KeyID:                "pam:mike.nestor",
		Principals:           []string{"mike.nestor", "mnestor"},
		ValidAfter:           time.Now(),
		ValidBefore:          time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("unexpected error recording the certificate: %v", err)
	}

	var line map[string]any
	if err := json.Unmarshal(shipped.Bytes(), &line); err != nil {
		t.Fatalf("failed to decode the shipped audit line %q: %v", shipped.String(), err)
	}
	if line["action"] != string(AuditCertIssued) {
		t.Fatalf("shipped line action = %v, want %s", line["action"], AuditCertIssued)
	}
	// The shipped line flattens the detail map into top-level attributes
	// (see AuditService.emit), so the claimed fields sit beside action.
	assertClaimedContext(t, AuditCertIssued, line, "mnestor", "web01")
}
