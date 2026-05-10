// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	types "github.com/mnestor/ssoossh/internal/api/response_types"
	mware "github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/store"
)

// mockAuditLogStore is an in-memory audit log for tests.
type mockAuditLogStore struct {
	entries []store.AuditLogEntry
	failFor string // if set, operations for this username return an error
}

func (m *mockAuditLogStore) Create(entry *store.AuditLogEntry) error {
	entry.ID = "mock-id-" + entry.RequestID
	entry.CreatedAt = time.Now()
	m.entries = append(m.entries, *entry)
	return nil
}

func (m *mockAuditLogStore) ListByUser(username string) ([]store.AuditLogEntry, error) {
	if m.failFor == username {
		return nil, errors.New("db error")
	}
	var result []store.AuditLogEntry
	for _, e := range m.entries {
		if e.UserName == username {
			result = append(result, e)
		}
	}
	return result, nil
}

// auditHandler wraps apiGetAuditLog with session middleware for the given user.
func auditHandler(sm *scs.SessionManager, username string, auditStore store.AuditLogInterface) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), mware.SessionContext, &mware.SessionManager{SessionManager: sm})
		ctx = context.WithValue(ctx, store.AuditLogContext, auditStore)
		sm.Put(ctx, "username", username)
		apiGetAuditLog(w, r.WithContext(ctx))
	})
	return sm.LoadAndSave(inner)
}

func TestApiGetAuditLog_Empty(t *testing.T) {
	sm := scs.New()
	auditStore := &mockAuditLogStore{}

	req, _ := http.NewRequest(http.MethodGet, "/audit", nil)
	rr := httptest.NewRecorder()
	auditHandler(sm, "alice", auditStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp types.AuditLogResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.StatusText != "success" {
		t.Errorf("expected status 'success', got %q", resp.StatusText)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(resp.Entries))
	}
}

func TestApiGetAuditLog_ReturnsOwnEntries(t *testing.T) {
	sm := scs.New()
	auditStore := &mockAuditLogStore{}
	_ = auditStore.Create(&store.AuditLogEntry{RequestID: "r1", UserName: "alice", Decision: "approved", CertType: "user"})
	_ = auditStore.Create(&store.AuditLogEntry{RequestID: "r2", UserName: "bob", Decision: "rejected", CertType: "user"})
	_ = auditStore.Create(&store.AuditLogEntry{RequestID: "r3", UserName: "alice", Decision: "rejected", CertType: "user"})

	req, _ := http.NewRequest(http.MethodGet, "/audit", nil)
	rr := httptest.NewRecorder()
	auditHandler(sm, "alice", auditStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp types.AuditLogResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Errorf("expected 2 entries for alice, got %d", len(resp.Entries))
	}
}

func TestApiGetAuditLog_DBError(t *testing.T) {
	sm := scs.New()
	auditStore := &mockAuditLogStore{failFor: "alice"}

	req, _ := http.NewRequest(http.MethodGet, "/audit", nil)
	rr := httptest.NewRecorder()
	auditHandler(sm, "alice", auditStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}
}
