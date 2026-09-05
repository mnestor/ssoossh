package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/model"
)

// The host context a module reports travels from the request body to the
// row, the approval page and every cert.* event. These tests pin the
// bounding on the way in and the shape of what each stage carries.

func int64p(v int64) *int64 { return &v }

// fullHostContext is a complete report, the shape the C module sends.
func fullHostContext() HostContext {
	at := time.Date(2026, 9, 5, 13, 4, 5, 0, time.UTC)
	return HostContext{
		RequestingUser:        "alice",
		Process:               "sudo -i",
		CallerUID:             int64p(1000),
		CallerPID:             int64p(4242),
		CallerPPID:            int64p(4200),
		MachineID:             "3f2c1e0d9b8a7f6e",
		OS:                    "Debian GNU/Linux 13 (trixie) Linux 6.12.0",
		Client:                "pam_ssoossh-c/0.3.0",
		Mode:                  "auto",
		ClientTime:            &at,
		TrustedCAFingerprints: []string{"SHA256:aaa", "SHA256:bbb"},
	}
}

// pamRequestWithContext creates a PAM request claiming account root on
// host web01, invoked by alice, with the full host context.
func pamRequestWithContext(t *testing.T, svc *CertRequestService) string {
	t.Helper()

	requestID, err := svc.createRequestID(context.Background(), NewCertRequestParams{
		Type:        model.CertificateTypePAM,
		PublicKey:   "ssh-ed25519 AAAA...",
		Username:    "root",
		SourceIP:    "198.51.100.7",
		Hostname:    "web01",
		PAMService:  "sudo",
		TTY:         "pts/3",
		HostContext: fullHostContext(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating the PAM request: %v", err)
	}
	return requestID
}

func TestApplyHostContext(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", maxContextFieldLen+50)
	tests := []struct {
		name  string
		in    HostContext
		check func(t *testing.T, req model.CertificateRequest)
	}{
		{
			name: "should copy every field onto the row",
			in:   fullHostContext(),
			check: func(t *testing.T, req model.CertificateRequest) {
				if req.RequestingUser != "alice" || req.Process != "sudo -i" || req.MachineID != "3f2c1e0d9b8a7f6e" ||
					req.OS != "Debian GNU/Linux 13 (trixie) Linux 6.12.0" || req.Client != "pam_ssoossh-c/0.3.0" || req.ClientMode != "auto" {
					t.Errorf("string fields not copied: %+v", req)
				}
				if req.CallerUID == nil || *req.CallerUID != 1000 || req.CallerPID == nil || *req.CallerPID != 4242 || req.CallerPPID == nil || *req.CallerPPID != 4200 {
					t.Errorf("process ids not copied: %v %v %v", req.CallerUID, req.CallerPID, req.CallerPPID)
				}
				if req.ClientTime == nil || !req.ClientTime.Equal(time.Date(2026, 9, 5, 13, 4, 5, 0, time.UTC)) {
					t.Errorf("client time not copied: %v", req.ClientTime)
				}
				if req.TrustedCAFingerprints != `["SHA256:aaa","SHA256:bbb"]` {
					t.Errorf("fingerprints = %q, want the JSON list", req.TrustedCAFingerprints)
				}
			},
		},
		{
			name: "should bound every string to the context field limit",
			in:   HostContext{RequestingUser: long, Process: long, MachineID: long, OS: long, Client: long, Mode: long, TrustedCAFingerprints: []string{long}},
			check: func(t *testing.T, req model.CertificateRequest) {
				for name, got := range map[string]string{
					"requesting_user": req.RequestingUser, "process": req.Process, "machine_id": req.MachineID,
					"os": req.OS, "client": req.Client, "client_mode": req.ClientMode,
				} {
					if len(got) != maxContextFieldLen {
						t.Errorf("%s has length %d, want %d", name, len(got), maxContextFieldLen)
					}
				}
				fps := decodeTrustedCAFingerprints(req.TrustedCAFingerprints)
				if len(fps) != 1 || len(fps[0]) != maxContextFieldLen {
					t.Errorf("fingerprint not bounded: %d entries, first length %d", len(fps), len(fps[0]))
				}
			},
		},
		{
			name: "should keep at most the first eight fingerprints",
			in:   HostContext{TrustedCAFingerprints: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}},
			check: func(t *testing.T, req model.CertificateRequest) {
				fps := decodeTrustedCAFingerprints(req.TrustedCAFingerprints)
				if len(fps) != maxTrustedCAFingerprints || fps[7] != "8" {
					t.Errorf("got %v, want the first %d", fps, maxTrustedCAFingerprints)
				}
			},
		},
		{
			name: "should store an empty column when nothing was reported",
			in:   HostContext{},
			check: func(t *testing.T, req model.CertificateRequest) {
				if req.TrustedCAFingerprints != "" {
					t.Errorf("fingerprints = %q, want empty", req.TrustedCAFingerprints)
				}
				if req.CallerUID != nil || req.ClientTime != nil {
					t.Errorf("expected nil pointers, got uid %v time %v", req.CallerUID, req.ClientTime)
				}
				if decodeTrustedCAFingerprints("") != nil {
					t.Error("expected an empty column to decode to nil")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var req model.CertificateRequest
			if err := applyHostContext(&req, tt.in); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, req)
		})
	}
}

func TestDecodeTrustedCAFingerprints_ShouldReadGarbageAsNothing(t *testing.T) {
	t.Parallel()

	if got := decodeTrustedCAFingerprints("not json"); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestHostContextDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  model.CertificateRequest
		want map[string]any
	}{
		{
			name: "should carry the PAM columns and the compact context for a pam request",
			req: model.CertificateRequest{
				Type: model.CertificateTypePAM, Username: "root", Hostname: "web01", PAMService: "sudo", TTY: "pts/3",
				RequestingUser: "alice", Process: "sudo -i", MachineID: "m1", Client: "c/1",
			},
			want: map[string]any{
				"username": "root", "hostname": "web01", "pam_service": "sudo", "tty": "pts/3", "remote_host": "",
				"requesting_user": "alice", "process": "sudo -i", "machine_id": "m1", "client": "c/1",
			},
		},
		{
			name: "should carry the local identity for a user request",
			req:  model.CertificateRequest{Type: model.CertificateTypeUser, LocalUsername: "alice", LocalHostname: "laptop", Username: "ignored"},
			want: map[string]any{
				"username": "alice", "hostname": "laptop", "pam_service": "", "tty": "", "remote_host": "",
				"requesting_user": "", "process": "", "machine_id": "", "client": "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := hostContextDetail(tt.req)
			if len(got) != len(tt.want) {
				t.Errorf("got %d keys, want %d: %v", len(got), len(tt.want), got)
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Errorf("detail[%q] = %v, want %v", k, got[k], want)
				}
			}
		})
	}
}

func TestFullHostContextDetail_ShouldAddTheLongTail(t *testing.T) {
	t.Parallel()

	req := model.CertificateRequest{
		Type: model.CertificateTypePAM, CallerUID: int64p(1000), OS: "os", ClientMode: "auto",
		LocalUsername: "unused", TrustedCAFingerprints: `["SHA256:aaa"]`,
	}
	got := fullHostContextDetail(req)
	for _, key := range []string{"local_username", "local_hostname", "caller_uid", "caller_pid", "caller_ppid", "os", "client_mode", "client_time", "trusted_ca_fingerprints"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing key %q", key)
		}
	}
	if fps, ok := got["trusted_ca_fingerprints"].([]string); !ok || len(fps) != 1 {
		t.Errorf("trusted_ca_fingerprints = %v, want the decoded list", got["trusted_ca_fingerprints"])
	}
}

// storedRow reads back the one stored event with the given action, row and
// payload together, so a test can check the grouping columns too.
func storedRow(t *testing.T, svc *CertRequestService, action AuditAction) (model.AuditEvent, AuditEvent) {
	t.Helper()

	var rows []model.AuditEvent
	if err := svc.db.Find(&rows).Error; err != nil {
		t.Fatalf("failed to load audit events: %v", err)
	}
	var matchedRow model.AuditEvent
	var matched []AuditEvent
	for _, row := range rows {
		if event := decodePayload(t, row); event.Action == action {
			matched = append(matched, event)
			matchedRow = row
		}
	}
	if len(matched) != 1 {
		t.Fatalf("got %d %s events, want exactly one", len(matched), action)
	}
	return matchedRow, matched[0]
}

func TestAudit_ShouldRecordTheWholeHostContextWhenAPAMRequestIsCreated(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)
	pamRequestWithContext(t, svc)

	detail := storedDetail(t, svc, AuditCertRequested)
	for key, want := range map[string]any{
		"username": "root", "hostname": "web01", "pam_service": "sudo", "tty": "pts/3",
		"requesting_user": "alice", "process": "sudo -i", "machine_id": "3f2c1e0d9b8a7f6e",
		"os": "Debian GNU/Linux 13 (trixie) Linux 6.12.0", "client": "pam_ssoossh-c/0.3.0", "client_mode": "auto",
		"source_ip": "198.51.100.7",
	} {
		if detail[key] != want {
			t.Errorf("cert.requested detail[%q] = %v, want %v", key, detail[key], want)
		}
	}
	// JSON round-trips integers as float64.
	if detail["caller_uid"] != float64(1000) {
		t.Errorf("caller_uid = %v, want 1000", detail["caller_uid"])
	}
	if fps, ok := detail["trusted_ca_fingerprints"].([]any); !ok || len(fps) != 2 {
		t.Errorf("trusted_ca_fingerprints = %v, want two entries", detail["trusted_ca_fingerprints"])
	}
	if _, present := detail["user_code"]; present {
		t.Error("the user code must never reach an audit payload")
	}
}

func TestAudit_ShouldRecordWhatAnApprovalGranted(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)
	requestID := pamRequestWithContext(t, svc)

	identity := pamApprover()
	seedUser(t, svc.db, identity.Subject)
	dc := DecisionContext{SourceIP: "203.0.113.9", UserAgent: "Mozilla/5.0 (approver)"}
	if err := svc.Approve(context.Background(), requestID, identity, dc, ApprovalSelection{Principals: []string{"mnestor"}}); err != nil {
		t.Fatalf("unexpected error approving the request: %v", err)
	}

	detail := storedDetail(t, svc, AuditCertApproved)
	principals, ok := detail["principals"].([]any)
	if !ok || len(principals) != 1 || principals[0] != "mnestor" {
		t.Errorf("cert.approved principals = %v, want [mnestor]", detail["principals"])
	}
	for key, want := range map[string]any{
		"approver_ip": "203.0.113.9", "approver_user_agent": "Mozilla/5.0 (approver)",
		"source_ip": "198.51.100.7", "requesting_user": "alice", "process": "sudo -i", "pam_service": "sudo",
	} {
		if detail[key] != want {
			t.Errorf("cert.approved detail[%q] = %v, want %v", key, detail[key], want)
		}
	}
	for _, key := range []string{"extensions", "force_command", "source_addresses", "no_touch_required"} {
		if _, present := detail[key]; !present {
			t.Errorf("cert.approved detail has no %q key", key)
		}
	}

	// The decisions row keeps the same content, and the request row now
	// shows the narrowed options rather than the ones asked for.
	var decision model.CertificateRequestDecision
	if err := svc.db.First(&decision, "certificate_request_id = ?", requestID).Error; err != nil {
		t.Fatalf("failed to read the decision: %v", err)
	}
	if decision.Principals != `["mnestor"]` {
		t.Errorf("decision principals = %q, want [\"mnestor\"]", decision.Principals)
	}
	var granted RequestedOptions
	if err := json.Unmarshal([]byte(decision.GrantedOptions), &granted); err != nil {
		t.Fatalf("decision granted_options %q does not decode: %v", decision.GrantedOptions, err)
	}
	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("failed to read the request: %v", err)
	}
	if req.RequestedOptions != decision.GrantedOptions {
		t.Errorf("request requested_options = %q, want the decision's granted options %q", req.RequestedOptions, decision.GrantedOptions)
	}
}

func TestAudit_ShouldGroupADenialUnderTheDenier(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)
	requestID := pamRequestWithContext(t, svc)

	identity := pamApprover()
	userID := seedUser(t, svc.db, identity.Subject)
	dc := DecisionContext{SourceIP: "203.0.113.9", UserAgent: "Mozilla/5.0 (denier)"}
	if err := svc.Deny(context.Background(), requestID, identity, dc); err != nil {
		t.Fatalf("unexpected error denying the request: %v", err)
	}

	row, event := storedRow(t, svc, AuditCertDenied)
	if row.ActorUserID == nil || *row.ActorUserID != userID {
		t.Errorf("actor_user_id = %v, want the denier's id %q", row.ActorUserID, userID)
	}
	for key, want := range map[string]any{
		"source_ip": "198.51.100.7", "approver_ip": "203.0.113.9", "approver_user_agent": "Mozilla/5.0 (denier)",
		"username": "root", "hostname": "web01", "requesting_user": "alice",
	} {
		if event.Detail[key] != want {
			t.Errorf("cert.denied detail[%q] = %v, want %v", key, event.Detail[key], want)
		}
	}
}

func TestAudit_ShouldStillRecordADenialWhenTheDenierHasNoUsersRow(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)
	requestID := pamRequestWithContext(t, svc)

	if err := svc.Deny(context.Background(), requestID, pamApprover(), DecisionContext{}); err != nil {
		t.Fatalf("unexpected error denying the request: %v", err)
	}
	row, _ := storedRow(t, svc, AuditCertDenied)
	if row.ActorUserID != nil {
		t.Errorf("actor_user_id = %q, want NULL for an unknown denier", *row.ActorUserID)
	}
}

func TestAudit_ShouldRecordAnExpiredRequest(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)
	requestID := pamRequestWithContext(t, svc)

	svc.expire(context.Background(), requestID)

	_, event := storedRow(t, svc, AuditCertExpired)
	if !event.System || event.Actor != nil {
		t.Errorf("cert.expired should be a system event with no actor, got system=%v actor=%v", event.System, event.Actor)
	}
	for key, want := range map[string]any{
		"request_id": requestID, "cert_type": "pam", "username": "root", "hostname": "web01", "source_ip": "198.51.100.7",
	} {
		if event.Detail[key] != want {
			t.Errorf("cert.expired detail[%q] = %v, want %v", key, event.Detail[key], want)
		}
	}

	// Expiring an already-resolved request changes nothing and says nothing.
	svc.expire(context.Background(), requestID)
	storedRow(t, svc, AuditCertExpired)
}

func TestAudit_ShouldRecordASignerRefusal(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)
	h := newTestSignedReplyHandler(t, svc)
	requestID := pamRequestWithContext(t, svc)
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusSigning).Error; err != nil {
		t.Fatalf("failed to move the request to signing: %v", err)
	}

	if err := deliver(t, h, certmsg.SignedReply{
		RequestID: requestID,
		Type:      model.CertificateTypePAM,
		Error:     "ssh-agent unreachable",
		ErrorCode: certmsg.ErrCodeCAUnavailable,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, event := storedRow(t, svc, AuditCertSignFailed)
	if !event.System {
		t.Error("cert.sign_failed should be a system event")
	}
	for key, want := range map[string]any{
		"request_id": requestID, "cert_type": "pam", "error_code": certmsg.ErrCodeCAUnavailable,
		"error": "ssh-agent unreachable", "username": "root", "hostname": "web01", "process": "sudo -i",
	} {
		if event.Detail[key] != want {
			t.Errorf("cert.sign_failed detail[%q] = %v, want %v", key, event.Detail[key], want)
		}
	}
}

func TestAudit_ShouldRecordAStrandedRequestAsASignFailure(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(10*time.Minute))
	withAuditTable(t, svc)
	requestID := signingRequestAged(t, svc, 30*time.Minute)

	if err := svc.SweepStrandedRequests(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, event := storedRow(t, svc, AuditCertSignFailed)
	for key, want := range map[string]any{
		"request_id": requestID, "error_code": "stranded", "error": FailureReasonStranded,
	} {
		if event.Detail[key] != want {
			t.Errorf("cert.sign_failed detail[%q] = %v, want %v", key, event.Detail[key], want)
		}
	}
}

func TestAudit_ShouldRecordTheFirstOpenOfTheApprovalPage(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	withAuditTable(t, svc)
	requestID := pamRequestWithContext(t, svc)

	claimPage(t, svc, requestID, "Mozilla/5.0 (claimer)")
	// A revisit with the token is a match, not a second claim.
	if _, err := svc.ClaimApprovalPage(context.Background(), requestID, "", "Mozilla/5.0 (claimer)"); err != nil {
		t.Fatalf("unexpected error revisiting: %v", err)
	}

	_, event := storedRow(t, svc, AuditCertClaimed)
	if event.Actor != nil {
		t.Error("cert.claimed should carry no actor: nobody has authenticated yet")
	}
	for key, want := range map[string]any{
		"request_id": requestID, "cert_type": "pam", "user_agent": "Mozilla/5.0 (claimer)",
		"username": "root", "hostname": "web01", "source_ip": "198.51.100.7",
	} {
		if event.Detail[key] != want {
			t.Errorf("cert.claimed detail[%q] = %v, want %v", key, event.Detail[key], want)
		}
	}
}

func TestAudit_ShouldCarryTheLocalIdentityWhenAUserCertificateIsIssued(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, pamAuditOptions())
	shipped := withAuditTable(t, svc)
	h := newTestSignedReplyHandler(t, svc)
	requestID, err := svc.createRequestID(context.Background(), NewCertRequestParams{
		Type:          model.CertificateTypeUser,
		PublicKey:     "ssh-ed25519 AAAA...",
		SourceIP:      "198.51.100.8",
		LocalUsername: "alice",
		LocalHostname: "alice-laptop",
	})
	if err != nil {
		t.Fatalf("unexpected error creating the user request: %v", err)
	}
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusSigning).Error; err != nil {
		t.Fatalf("failed to move the request to signing: %v", err)
	}
	shipped.Reset()

	if _, err := h.recordCertificate(context.Background(), certmsg.SignedReply{
		RequestID:            requestID,
		Type:                 model.CertificateTypeUser,
		PublicKeyFingerprint: "SHA256:test",
		Serial:               43,
		KeyID:                "alice",
		Principals:           []string{"alice"},
		Extensions:           []string{"permit-pty"},
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
	// A user-type request keeps its requester in the local_* columns; the
	// event must read those rather than the empty PAM ones.
	assertClaimedContext(t, AuditCertIssued, line, "alice", "alice-laptop")
	if line["source_ip"] != "198.51.100.8" {
		t.Errorf("cert.issued source_ip = %v, want the requester's", line["source_ip"])
	}
	if exts, ok := line["extensions"].([]any); !ok || len(exts) != 1 || exts[0] != "permit-pty" {
		t.Errorf("cert.issued extensions = %v, want [permit-pty]", line["extensions"])
	}
}
