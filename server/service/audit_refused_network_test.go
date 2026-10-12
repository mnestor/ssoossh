package service

// Test methodology: CreateRequest over the in-memory harness with a real
// recorder attached (withAuditTable), so a refusal is read back from the
// table and from the shipped-log buffer as a reviewer would see it. The
// sweep tests seed the audit table through Record and read the survivors
// back by a per-row label in the detail. Database failures are injected
// with gorm callbacks, which work on both test backends.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// gatedConsoleNetwork is the only network the refusal tests' console type
// accepts requests from.
const gatedConsoleNetwork = "10.20.0.0/16"

// refusedNetworkService is a console-gated service with a real recorder
// attached. It returns the shipped-log buffer alongside.
func refusedNetworkService(t *testing.T) (*CertRequestService, *bytes.Buffer) {
	t.Helper()

	svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute, gatedConsoleNetwork))
	return svc, withAuditTable(t, svc)
}

// consoleParams is a console request from sourceIP claiming account root on
// host web01.
func consoleParams(sourceIP string) NewCertRequestParams {
	return NewCertRequestParams{
		Type:      model.CertificateTypeConsole,
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey console@web01",
		Username:  "root",
		Hostname:  "web01",
		SourceIP:  sourceIP,
	}
}

// refusalRows returns the stored cert.request_refused_network rows.
func refusalRows(t *testing.T, db *gorm.DB) []model.AuditEvent {
	t.Helper()

	var rows []model.AuditEvent
	if err := db.Where("action = ?", string(AuditCertRequestRefusedNetwork)).Find(&rows).Error; err != nil {
		t.Fatalf("failed to load refusal rows: %v", err)
	}
	return rows
}

// countCertRequests counts the persisted certificate requests.
func countCertRequests(t *testing.T, db *gorm.DB) int64 {
	t.Helper()

	var n int64
	if err := db.Model(&model.CertificateRequest{}).Count(&n).Error; err != nil {
		t.Fatalf("failed to count certificate requests: %v", err)
	}
	return n
}

func TestCreateRequest_ShouldAuditARequestRefusedForItsNetwork(t *testing.T) {
	t.Parallel()

	svc, _ := refusedNetworkService(t)
	_, err := svc.CreateRequest(context.Background(), consoleParams("203.0.113.9"))
	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("got error %v, want a ForbiddenError", err)
	}

	rows := refusalRows(t, svc.db)
	if len(rows) != 1 {
		t.Fatalf("got %d refusal rows, want exactly 1", len(rows))
	}
	row := rows[0]
	event := decodePayload(t, row)

	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "should record the refusal action", got: event.Action, want: AuditCertRequestRefusedNetwork},
		{name: "should index the refusal action", got: row.Action, want: string(AuditCertRequestRefusedNetwork)},
		{name: "should mark it a system event", got: event.System, want: true},
		{name: "should record no actor", got: event.Actor == nil, want: true},
		{name: "should index no actor", got: row.ActorUserID == nil, want: true},
		{name: "should record no target", got: event.Target == nil, want: true},
		{name: "should record the certificate type", got: event.Detail["cert_type"], want: "console"},
		{name: "should record the observed source address", got: event.Detail["source_ip"], want: "203.0.113.9"},
		{name: "should record the reason", got: event.Reason, want: "the source address is outside cert_options.console.allowed_networks"},
		{name: "should label the username as claimed", got: event.Detail["claimed_username"], want: "root"},
		{name: "should label the hostname as claimed", got: event.Detail["claimed_hostname"], want: "web01"},
		{name: "should not record an unlabelled username", got: event.Detail["username"], want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

// The reason must keep the allowed networks to itself: it is deployment
// topology and the caller is unauthenticated.
func TestCreateRequest_ShouldKeepTheRefusalTextUnchanged(t *testing.T) {
	t.Parallel()

	svc, _ := refusedNetworkService(t)
	_, err := svc.CreateRequest(context.Background(), consoleParams("203.0.113.9"))

	want := "console certificate requests are not accepted from this network"
	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) || forbidden.Reason != want {
		t.Errorf("got error %v, want a ForbiddenError reading %q", err, want)
	}
}

func TestCreateRequest_ShouldWriteOneRefusalRowPerRefusedRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		refusals  int
		wantRows  int
		wantCerts int64
	}{
		{name: "should write one row when one request is refused", refusals: 1, wantRows: 1},
		{name: "should write three rows when three requests are refused", refusals: 3, wantRows: 3},
		{name: "should write ten rows when ten requests are refused", refusals: 10, wantRows: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, _ := refusedNetworkService(t)
			for i := range tt.refusals {
				if _, err := svc.CreateRequest(context.Background(), consoleParams(fmt.Sprintf("203.0.113.%d", i+1))); err == nil {
					t.Fatalf("refusal %d: expected the network gate to refuse the request", i)
				}
			}

			if got := len(refusalRows(t, svc.db)); got != tt.wantRows {
				t.Errorf("got %d refusal rows, want %d", got, tt.wantRows)
			}
			if got := countCertRequests(t, svc.db); got != tt.wantCerts {
				t.Errorf("got %d certificate requests persisted, want %d", got, tt.wantCerts)
			}
		})
	}
}

func TestCreateRequest_ShouldNotAuditARefusalWhenTheSourceIsAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		networks []string
		certType model.CertificateType
		sourceIP string
	}{
		{name: "should write no refusal row when the source is inside the gate", networks: []string{gatedConsoleNetwork}, certType: model.CertificateTypeConsole, sourceIP: "10.20.3.4"},
		{name: "should write no refusal row when the type has no gate", certType: model.CertificateTypeConsole, sourceIP: "203.0.113.9"},
		{name: "should write no refusal row when another type is gated", networks: []string{gatedConsoleNetwork}, certType: model.CertificateTypePAM, sourceIP: "203.0.113.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestCertRequestServiceWithOptions(t, consoleOptions(5*time.Minute, 2*time.Minute, tt.networks...))
			withAuditTable(t, svc)
			p := consoleParams(tt.sourceIP)
			p.Type = tt.certType
			if _, err := svc.CreateRequest(context.Background(), p); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := len(refusalRows(t, svc.db)); got != 0 {
				t.Errorf("got %d refusal rows, want none", got)
			}
		})
	}
}

// Both sinks: the table copy the UI reads and the shipped line that is the
// archive.
func TestCreateRequest_ShouldShipTheRefusalToTheAuditLog(t *testing.T) {
	t.Parallel()

	svc, shipped := refusedNetworkService(t)
	if _, err := svc.CreateRequest(context.Background(), consoleParams("203.0.113.9")); err == nil {
		t.Fatal("expected the network gate to refuse the request")
	}

	var lines []map[string]any
	for line := range strings.Lines(shipped.String()) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("failed to decode shipped line %q: %v", line, err)
		}
		if entry["action"] == string(AuditCertRequestRefusedNetwork) {
			lines = append(lines, entry)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("got %d shipped refusal lines, want exactly 1:\n%s", len(lines), shipped.String())
	}
	if got := lines[0]["source_ip"]; got != "203.0.113.9" {
		t.Errorf("shipped source_ip = %v, want 203.0.113.9", got)
	}
}

// The claimed fields come from an unauthenticated caller, so they are bounded
// like the stored context fields.
func TestCreateRequest_ShouldBoundTheClaimedFieldsOfARefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		build func(p *NewCertRequestParams, v string)
	}{
		{name: "should bound the claimed username when it is too long", key: "claimed_username", build: func(p *NewCertRequestParams, v string) { p.Username = v }},
		{name: "should bound the claimed hostname when it is too long", key: "claimed_hostname", build: func(p *NewCertRequestParams, v string) { p.Hostname = v }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, _ := refusedNetworkService(t)
			p := consoleParams("203.0.113.9")
			tt.build(&p, strings.Repeat("a", maxContextFieldLen+50))
			if _, err := svc.CreateRequest(context.Background(), p); err == nil {
				t.Fatal("expected the network gate to refuse the request")
			}

			rows := refusalRows(t, svc.db)
			if len(rows) != 1 {
				t.Fatalf("got %d refusal rows, want 1", len(rows))
			}
			got, _ := decodePayload(t, rows[0]).Detail[tt.key].(string)
			if len(got) != maxContextFieldLen {
				t.Errorf("%s is %d bytes, want %d", tt.key, len(got), maxContextFieldLen)
			}
		})
	}
}

// A caller that hangs up straight after sending must still leave its row.
func TestCreateRequest_ShouldAuditARefusalWhenTheCallerHasGoneAway(t *testing.T) {
	t.Parallel()

	svc, _ := refusedNetworkService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.CreateRequest(ctx, consoleParams("203.0.113.9")); err == nil {
		t.Fatal("expected the network gate to refuse the request")
	}

	if got := len(refusalRows(t, svc.db)); got != 1 {
		t.Errorf("got %d refusal rows from a cancelled request, want 1", got)
	}
}

// sweepRow is one seeded audit row: a label to find it by afterwards, its
// action, and how long ago it happened.
type sweepRow struct {
	label  string
	action AuditAction
	age    time.Duration
}

// seedSweepRows records rows through the real recorder.
func seedSweepRows(t *testing.T, audit *AuditService, rows []sweepRow) {
	t.Helper()

	now := time.Now()
	for _, r := range rows {
		event := AuditEvent{
			Action:     r.action,
			OccurredAt: now.Add(-r.age),
			Detail:     map[string]any{"label": r.label},
		}
		if r.action == AuditCertRequestRefusedNetwork {
			event.System = true
		} else {
			event.Actor = &AuditSubject{UserID: "u-alice"}
		}
		if reasonRequired[r.action] {
			event.Reason = "offboarded"
		}
		audit.Record(context.Background(), event)
	}
}

// survivingLabels returns the labels of the rows left in the table, sorted.
func survivingLabels(t *testing.T, db *gorm.DB) []string {
	t.Helper()

	var rows []model.AuditEvent
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("failed to load audit events: %v", err)
	}
	labels := make([]string, 0, len(rows))
	for _, row := range rows {
		label, _ := decodePayload(t, row).Detail["label"].(string)
		labels = append(labels, label)
	}
	slices.Sort(labels)
	return labels
}

// refusalSweepCase is one row-cap sweep: the rows seeded, the cap, and the
// labels that must survive.
type refusalSweepCase struct {
	name    string
	rows    []sweepRow
	maxRows int64
	want    []string
}

// refusalSweepCases are the row-cap cases shared by the SQLite test below and
// the Postgres one in audit_refused_network_pg_test.go. Network refusals get
// no special treatment: the sweep is oldest first whatever the action.
func refusalSweepCases() []refusalSweepCase {
	return []refusalSweepCase{
		{
			name: "should delete older events of other actions before a newer burst of refusals",
			rows: []sweepRow{
				{label: "approved", action: AuditCertApproved, age: 5 * time.Hour},
				{label: "disabled", action: AuditUserDisabled, age: 4 * time.Hour},
				{label: "login", action: AuditAuthLogin, age: 3 * time.Hour},
				{label: "refusal-1", action: AuditCertRequestRefusedNetwork, age: 3 * time.Minute},
				{label: "refusal-2", action: AuditCertRequestRefusedNetwork, age: 2 * time.Minute},
				{label: "refusal-3", action: AuditCertRequestRefusedNetwork, age: time.Minute},
			},
			maxRows: 3,
			want:    []string{"refusal-1", "refusal-2", "refusal-3"},
		},
		{
			name: "should delete an older refusal before newer events of other actions",
			rows: []sweepRow{
				{label: "refusal-1", action: AuditCertRequestRefusedNetwork, age: 6 * time.Hour},
				{label: "approved", action: AuditCertApproved, age: 5 * time.Hour},
				{label: "login", action: AuditAuthLogin, age: 3 * time.Hour},
			},
			maxRows: 2,
			want:    []string{"approved", "login"},
		},
		{
			name: "should keep a newer refusal while deleting older events of other actions",
			rows: []sweepRow{
				{label: "approved", action: AuditCertApproved, age: 5 * time.Hour},
				{label: "disabled", action: AuditUserDisabled, age: 4 * time.Hour},
				{label: "login", action: AuditAuthLogin, age: 3 * time.Hour},
				{label: "refusal-1", action: AuditCertRequestRefusedNetwork, age: time.Minute},
			},
			maxRows: 2,
			want:    []string{"login", "refusal-1"},
		},
		{
			name: "should delete oldest first when there are no refusals",
			rows: []sweepRow{
				{label: "approved", action: AuditCertApproved, age: 5 * time.Hour},
				{label: "disabled", action: AuditUserDisabled, age: 4 * time.Hour},
				{label: "login", action: AuditAuthLogin, age: 3 * time.Hour},
			},
			maxRows: 1,
			want:    []string{"login"},
		},
		{
			name: "should delete nothing when the table is at its cap",
			rows: []sweepRow{
				{label: "approved", action: AuditCertApproved, age: 5 * time.Hour},
				{label: "refusal-1", action: AuditCertRequestRefusedNetwork, age: time.Minute},
			},
			maxRows: 2,
			want:    []string{"approved", "refusal-1"},
		},
	}
}

// checkRefusalSweep seeds tt into db's audit table, sweeps it, and checks
// which rows survived.
func checkRefusalSweep(t *testing.T, db *gorm.DB, tt refusalSweepCase) {
	t.Helper()

	seedSweepRows(t, NewAuditService(&config.Config{}, db), tt.rows)
	if err := SweepAuditEvents(context.Background(), db, 0, tt.maxRows); err != nil {
		t.Fatalf("SweepAuditEvents() error = %v", err)
	}

	want := slices.Clone(tt.want)
	slices.Sort(want)
	if got := survivingLabels(t, db); !slices.Equal(got, want) {
		t.Errorf("rows left after the sweep = %v, want %v", got, want)
	}
}

func TestSweepAuditEvents_ShouldDeleteOldestFirstWhateverTheAction(t *testing.T) {
	t.Parallel()

	for _, tt := range refusalSweepCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, db := newTestAuditService(t)
			checkRefusalSweep(t, db, tt)
		})
	}
}

// An excess larger than one batch is still deleted in full, and nothing
// newer goes with it. The burst is the oldest rows, so all of it is excess.
func TestSweepAuditEvents_ShouldDeleteAnExcessLargerThanOneBatch(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)
	seedSweepRows(t, audit, []sweepRow{
		{label: "approved", action: AuditCertApproved, age: 5 * time.Hour},
		{label: "disabled", action: AuditUserDisabled, age: 4 * time.Hour},
	})
	burst := make([]model.AuditEvent, 0, 2*auditSweepBatch+7)
	now := time.Now()
	for i := range cap(burst) {
		row, _, err := audit.prepare(AuditEvent{
			Action:     AuditCertRequestRefusedNetwork,
			System:     true,
			OccurredAt: now.Add(-10*time.Hour - time.Duration(i)*time.Millisecond),
			Detail:     map[string]any{"label": "refusal"},
		})
		if err != nil {
			t.Fatalf("prepare refusal %d: %v", i, err)
		}
		burst = append(burst, *row)
	}
	if err := db.CreateInBatches(burst, 200).Error; err != nil {
		t.Fatalf("seed the refusal burst: %v", err)
	}

	if err := SweepAuditEvents(context.Background(), db, 0, 2); err != nil {
		t.Fatalf("SweepAuditEvents() error = %v", err)
	}

	if got := survivingLabels(t, db); !slices.Equal(got, []string{"approved", "disabled"}) {
		t.Errorf("rows left after the sweep = %v, want only the two other events", got)
	}
}

// failAuditStatement makes every statement of one kind that match selects
// fail after it runs, so the sweep's error path is reached on either test
// backend.
func failAuditStatement(t *testing.T, db *gorm.DB, kind string, match func(sql string) bool) {
	t.Helper()

	inject := func(tx *gorm.DB) {
		if match(tx.Statement.SQL.String()) {
			_ = tx.AddError(errors.New("injected failure"))
		}
	}
	var err error
	switch kind {
	case "query":
		err = db.Callback().Query().After("gorm:query").Register("test:fail_audit_query", inject)
	case "delete":
		err = db.Callback().Delete().After("gorm:delete").Register("test:fail_audit_delete", inject)
	default:
		t.Fatalf("unknown statement kind %q", kind)
	}
	if err != nil {
		t.Fatalf("register the failure callback: %v", err)
	}
}

func TestSweepAuditEvents_ShouldReportAFailureToPruneThePastCapRows(t *testing.T) {
	t.Parallel()

	oldestSelect := func(sql string) bool { return strings.Contains(sql, "ORDER BY created_at ASC, id ASC") }
	anyDelete := func(string) bool { return true }

	tests := []struct {
		name  string
		kind  string
		match func(string) bool
	}{
		{name: "should report a failure when the select fails", kind: "query", match: oldestSelect},
		{name: "should report a failure when the delete fails", kind: "delete", match: anyDelete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			audit, db := newTestAuditService(t)
			seedSweepRows(t, audit, []sweepRow{
				{label: "approved", action: AuditCertApproved, age: 5 * time.Hour},
				{label: "login", action: AuditAuthLogin, age: 3 * time.Hour},
				{label: "refusal-1", action: AuditCertRequestRefusedNetwork, age: time.Minute},
			})
			failAuditStatement(t, db, tt.kind, tt.match)

			const wantErr = "failed to prune audit events past the row cap"
			err := SweepAuditEvents(context.Background(), db, 0, 1)
			if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Errorf("SweepAuditEvents() error = %v, want one containing %q", err, wantErr)
			}
		})
	}
}
