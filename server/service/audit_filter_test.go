package service

// Test methodology: table-driven unit tests over an in-memory sqlite
// database, through the real recorder. Two properties matter. Every stored
// row carries the event's observed source address in the indexed source_ip
// column, and nothing else (never approver_ip, never a reported host). And
// the feed's two filters are exact matches that page like the unfiltered
// feed does.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// recordWriters are the two write paths every stored audit row goes
// through. Both call prepare, which is where the column is set.
func recordWriters() []struct {
	name  string
	write func(t *testing.T, audit *AuditService, db *gorm.DB, event AuditEvent)
} {
	return []struct {
		name  string
		write func(t *testing.T, audit *AuditService, db *gorm.DB, event AuditEvent)
	}{
		{
			name: "Record",
			write: func(_ *testing.T, audit *AuditService, _ *gorm.DB, event AuditEvent) {
				audit.Record(context.Background(), event)
			},
		},
		{
			name: "RecordTx",
			write: func(t *testing.T, audit *AuditService, db *gorm.DB, event AuditEvent) {
				if err := db.Transaction(func(tx *gorm.DB) error {
					return audit.RecordTx(tx, event)
				}); err != nil {
					t.Fatalf("RecordTx() error = %v", err)
				}
			},
		},
	}
}

// Every action, on both write paths: the column holds detail.source_ip when
// the event has one, and is empty when it does not. The other addresses an event
// can carry (the approver's connection, the host the client reported) are
// present in every case, so a recorder that picked one of them up instead
// fails here.
func TestRecord_ShouldSetTheSourceIPColumnToTheObservedSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		detail map[string]any
		want   string
	}{
		{
			name: "with an observed source",
			detail: map[string]any{
				"source_ip":   "192.0.2.7",
				"approver_ip": "198.51.100.9",
				"remote_host": "203.0.113.5",
			},
			want: "192.0.2.7",
		},
		{
			name: "with no observed source",
			detail: map[string]any{
				"approver_ip": "198.51.100.9",
				"remote_host": "203.0.113.5",
			},
			want: "",
		},
	}

	for _, action := range auditTaxonomy(t) {
		if tableSkipped(action) {
			continue
		}
		for _, writer := range recordWriters() {
			for _, c := range cases {
				t.Run("should store the source of "+string(action)+" "+c.name+" when written by "+writer.name, func(t *testing.T) {
					t.Parallel()

					audit, db := newTestAuditService(t)
					writer.write(t, audit, db, AuditEvent{
						Action: action,
						Actor:  &AuditSubject{UserID: "u-actor"},
						Reason: "ticket 42",
						Detail: c.detail,
					})

					var rows []model.AuditEvent
					if err := db.Find(&rows).Error; err != nil {
						t.Fatalf("load audit events: %v", err)
					}
					if len(rows) != 1 {
						t.Fatalf("got %d rows, want 1", len(rows))
					}
					if rows[0].SourceIP != c.want {
						t.Errorf("source_ip column = %q, want %q", rows[0].SourceIP, c.want)
					}
				})
			}
		}
	}
}

func TestAuditSourceIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		detail map[string]any
		want   string
	}{
		{name: "should be empty when there is no detail", detail: nil, want: ""},
		{name: "should be empty when the detail has no source_ip", detail: map[string]any{"cert_type": "user"}, want: ""},
		{name: "should be empty when source_ip is empty", detail: map[string]any{"source_ip": ""}, want: ""},
		{name: "should be empty when source_ip is not a string", detail: map[string]any{"source_ip": 7}, want: ""},
		{name: "should be empty when source_ip is not an address", detail: map[string]any{"source_ip": "example.com"}, want: ""},
		{name: "should copy an IPv4 address", detail: map[string]any{"source_ip": "192.0.2.7"}, want: "192.0.2.7"},
		{name: "should copy an IPv6 address in canonical form", detail: map[string]any{"source_ip": "2001:DB8:0:0::1"}, want: "2001:db8::1"},
		{name: "should unmap an IPv4-mapped IPv6 address", detail: map[string]any{"source_ip": "::ffff:192.0.2.7"}, want: "192.0.2.7"},
		{name: "should ignore approver_ip when source_ip is absent", detail: map[string]any{"approver_ip": "198.51.100.9"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := auditSourceIP(tt.detail); got != tt.want {
				t.Errorf("auditSourceIP(%v) = %q, want %q", tt.detail, got, tt.want)
			}
		})
	}
}

// The filter list and the taxonomy are the same set, in the same order, so
// a new action cannot ship unfilterable.
func TestAuditActions_ShouldListEveryTaxonomyAction(t *testing.T) {
	t.Parallel()

	if want := auditTaxonomy(t); !slices.Equal(auditActions, want) {
		t.Errorf("auditActions = %v\nwant the taxonomy %v", auditActions, want)
	}
}

func TestParseAuditFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		action   string
		sourceIP string
		want     AuditFilter
		wantErr  bool
	}{
		{name: "should be no filter when both are empty", want: AuditFilter{}},
		{name: "should be no filter when both are whitespace", action: "  ", sourceIP: "\t", want: AuditFilter{}},
		{name: "should keep a known action", action: "cert.request_refused_network", want: AuditFilter{Action: AuditCertRequestRefusedNetwork}},
		{name: "should trim a known action", action: " auth.login ", want: AuditFilter{Action: AuditAuthLogin}},
		{name: "should accept a shipped-log-only action", action: "cert.issued", want: AuditFilter{Action: AuditCertIssued}},
		{name: "should accept an action no longer emitted", action: "admin.config_viewed", want: AuditFilter{Action: AuditAdminConfigViewed}},
		{name: "should reject an unknown action", action: "cert.nonsense", wantErr: true},
		{name: "should reject an action in the wrong case", action: "AUTH.LOGIN", wantErr: true},
		{name: "should reject the unreadable-row marker", action: "audit.unreadable", wantErr: true},
		{name: "should keep an IPv4 address", sourceIP: "192.0.2.7", want: AuditFilter{SourceIP: "192.0.2.7"}},
		{name: "should canonicalize an IPv6 address", sourceIP: "2001:DB8::0:1", want: AuditFilter{SourceIP: "2001:db8::1"}},
		{name: "should unmap an IPv4-mapped address", sourceIP: "::ffff:192.0.2.7", want: AuditFilter{SourceIP: "192.0.2.7"}},
		{name: "should reject a hostname", sourceIP: "example.com", wantErr: true},
		{name: "should reject a CIDR", sourceIP: "192.0.2.0/24", wantErr: true},
		{name: "should reject an address with a port", sourceIP: "192.0.2.7:22", wantErr: true},
		{name: "should reject an out-of-range octet", sourceIP: "192.0.2.256", wantErr: true},
		{name: "should keep both together", action: "cert.requested", sourceIP: "192.0.2.7", want: AuditFilter{Action: AuditCertRequested, SourceIP: "192.0.2.7"}},
		{name: "should reject both when only the address is bad", action: "cert.requested", sourceIP: "nope", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseAuditFilter(tt.action, tt.sourceIP)
			if tt.wantErr {
				var invalid *errorresponses.InvalidRequestError
				if !errors.As(err, &invalid) {
					t.Fatalf("ParseAuditFilter(%q, %q) error = %v, want an InvalidRequestError", tt.action, tt.sourceIP, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAuditFilter(%q, %q) error = %v", tt.action, tt.sourceIP, err)
			}
			if got != tt.want {
				t.Errorf("ParseAuditFilter(%q, %q) = %+v, want %+v", tt.action, tt.sourceIP, got, tt.want)
			}
		})
	}
}

// seedFilterFeed records a fixed mix of actions and sources, one minute
// apart, oldest first. Each event carries its index in detail.n so a test
// can name the rows it expects.
func seedFilterFeed(t *testing.T, audit *AuditService) {
	t.Helper()

	rows := []struct {
		action AuditAction
		source string
	}{
		{AuditCertRequestRefusedNetwork, "192.0.2.7"},    // 0
		{AuditCertRequested, "198.51.100.9"},             // 1
		{AuditCertRequestRefusedNetwork, "192.0.2.7"},    // 2
		{AuditCertRequestRefusedNetwork, "198.51.100.9"}, // 3
		{AuditAuthLogin, ""},                             // 4
		{AuditCertRequested, "192.0.2.7"},                // 5
		{AuditCertRequestRefusedNetwork, "192.0.2.7"},    // 6
	}
	base := time.Now().Add(-time.Hour)
	for i, row := range rows {
		detail := map[string]any{"n": i}
		if row.source != "" {
			detail["source_ip"] = row.source
		}
		audit.Record(context.Background(), AuditEvent{
			Action:     row.action,
			System:     true,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Detail:     detail,
		})
	}
}

// eventNumbers reads detail.n off a page, in page order.
func eventNumbers(page *AuditPage) []int {
	out := make([]int, 0, len(page.Events))
	for _, e := range page.Events {
		n, _ := e.Event.Detail["n"].(float64)
		out = append(out, int(n))
	}
	return out
}

func TestListRecent_ShouldFilterByActionAndSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		filter     AuditFilter
		limit      int
		offset     int
		want       []int
		wantTotal  int64
		wantOffset int
	}{
		{name: "should return everything newest first when unfiltered", limit: 25, want: []int{6, 5, 4, 3, 2, 1, 0}, wantTotal: 7},
		{name: "should keep one action when filtered by action", filter: AuditFilter{Action: AuditCertRequestRefusedNetwork}, limit: 25, want: []int{6, 3, 2, 0}, wantTotal: 4},
		{name: "should keep one source when filtered by source", filter: AuditFilter{SourceIP: "192.0.2.7"}, limit: 25, want: []int{6, 5, 2, 0}, wantTotal: 4},
		{name: "should keep the intersection when filtered by both", filter: AuditFilter{Action: AuditCertRequestRefusedNetwork, SourceIP: "192.0.2.7"}, limit: 25, want: []int{6, 2, 0}, wantTotal: 3},
		{name: "should return nothing when no row matches", filter: AuditFilter{SourceIP: "203.0.113.1"}, limit: 25, want: []int{}, wantTotal: 0},
		{name: "should return nothing for an action with no rows", filter: AuditFilter{Action: AuditUserDisabled}, limit: 25, want: []int{}, wantTotal: 0},
		{name: "should page the first window of a filtered feed", filter: AuditFilter{Action: AuditCertRequestRefusedNetwork}, limit: 2, want: []int{6, 3}, wantTotal: 4, wantOffset: 2},
		{name: "should page the last window of a filtered feed", filter: AuditFilter{Action: AuditCertRequestRefusedNetwork}, limit: 2, offset: 2, want: []int{2, 0}, wantTotal: 4},
		{name: "should page both filters together", filter: AuditFilter{Action: AuditCertRequestRefusedNetwork, SourceIP: "192.0.2.7"}, limit: 1, offset: 1, want: []int{2}, wantTotal: 3, wantOffset: 2},
	}

	audit, _ := newTestAuditService(t)
	seedFilterFeed(t, audit)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			page, err := audit.ListRecent(context.Background(), tt.filter, tt.limit, tt.offset)
			if err != nil {
				t.Fatalf("ListRecent() error = %v", err)
			}
			if got := eventNumbers(page); !slices.Equal(got, tt.want) {
				t.Errorf("events = %v, want %v", got, tt.want)
			}
			if page.Total != tt.wantTotal {
				t.Errorf("total = %d, want %d", page.Total, tt.wantTotal)
			}
			if page.NextOffset != tt.wantOffset {
				t.Errorf("next offset = %d, want %d", page.NextOffset, tt.wantOffset)
			}
		})
	}
}

// A row stored before the column existed, or by anything that bypassed the
// recorder, holds an empty source_ip and must not match an address filter.
func TestListRecent_ShouldNotMatchARowWithNoSource(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)
	if err := db.Create(&model.AuditEvent{
		ID:        "ae-blank",
		CreatedAt: time.Now(),
		Action:    string(AuditCertRequested),
		Payload:   `{"action":"cert.requested","detail":{"source_ip":"192.0.2.7"},"v":1}`,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	page, err := audit.ListRecent(context.Background(), AuditFilter{SourceIP: "192.0.2.7"}, 25, 0)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if page.Total != 0 {
		t.Errorf("total = %d, want 0: the filter reads the column, not the payload", page.Total)
	}
}
