// Created by Mike Nestor <me@mikenestor.org>
package httpd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	mware "github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/config"
	"github.com/mnestor/ssoossh/internal/store"
)

func init() {
	getApproveConfig = func() *config.Config {
		return &config.Config{}
	}
}

// -- Minimal mock stores --

type approveTestCertRequestStore struct {
	requests map[string]store.CertRequest
}

func newApproveTestCertRequestStore(req store.CertRequest) *approveTestCertRequestStore {
	return &approveTestCertRequestStore{requests: map[string]store.CertRequest{req.ID: req}}
}

func (m *approveTestCertRequestStore) Get(id string) (*store.CertRequest, error) {
	r, ok := m.requests[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &r, nil
}
func (m *approveTestCertRequestStore) Create(c *store.CertRequest) error { return nil }
func (m *approveTestCertRequestStore) Delete(id string) error {
	delete(m.requests, id)
	return nil
}

type approveTestCertStore struct {
	certs []store.Certificate
	subs  map[string]chan error
}

func newApproveTestCertStore() *approveTestCertStore {
	return &approveTestCertStore{subs: map[string]chan error{}}
}

func (m *approveTestCertStore) Get(id string) (*store.Certificate, error) {
	for _, c := range m.certs {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, errors.New("not found")
}
func (m *approveTestCertStore) Create(c *store.Certificate) error {
	c.CreatedAt = time.Now()
	m.certs = append(m.certs, *c)
	if ch, ok := m.subs[c.ID]; ok {
		ch <- nil
	}
	return nil
}
func (m *approveTestCertStore) Delete(id string) error { return nil }
func (m *approveTestCertStore) GetWait(id string) *store.Subscriber {
	ch := make(chan error, 1)
	m.subs[id] = ch
	return &store.Subscriber{ID: id, Phone: ch}
}
func (m *approveTestCertStore) Reject(id string) error {
	if ch, ok := m.subs[id]; ok {
		ch <- errors.New("rejected")
	}
	return nil
}

type approveTestAuditStore struct {
	entries []store.AuditLogEntry
}

func (m *approveTestAuditStore) Create(e *store.AuditLogEntry) error {
	e.ID = "audit-id"
	m.entries = append(m.entries, *e)
	return nil
}
func (m *approveTestAuditStore) ListByUser(username string) ([]store.AuditLogEntry, error) {
	return m.entries, nil
}

// -- Helper --

const testPubKey = `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GkZH test`

// approveHandler wraps fn with session middleware and injects stores into context.
func approveHandler(fn http.HandlerFunc, id string, reqStore store.CertRequestInterface, certStore store.CertificateInterface, auditStore store.AuditLogInterface) http.Handler {
	sm := scs.New()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), mware.SessionContext, &mware.SessionManager{SessionManager: sm})
		ctx = context.WithValue(ctx, store.CertRequestContext, reqStore)
		ctx = context.WithValue(ctx, store.CertificateContext, certStore)
		ctx = context.WithValue(ctx, store.AuditLogContext, auditStore)
		sm.Put(ctx, "username", "testuser")

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

		fn(w, r.WithContext(ctx))
	})
	return sm.LoadAndSave(inner)
}

// -- Tests --

func TestApiGetApprove_DeletesRequest(t *testing.T) {
	certReq := store.CertRequest{
		ID:     "req-1",
		Pubkey: testPubKey,
		Type:   "user",
	}
	reqStore := newApproveTestCertRequestStore(certReq)
	certStore := newApproveTestCertStore()
	auditStore := &approveTestAuditStore{}

	req, _ := http.NewRequest(http.MethodGet, "/approve/req-1", nil)
	rr := httptest.NewRecorder()
	approveHandler(apiGetApprove, "req-1", reqStore, certStore, auditStore).ServeHTTP(rr, req)

	// The cert request should have been deleted (even if signing fails due to no SSH key).
	if _, err := reqStore.Get("req-1"); err == nil {
		t.Error("expected cert request to be deleted after approve, but it still exists")
	}
}

func TestApiGetReject_WritesAuditAndSignalsChan(t *testing.T) {
	certReq := store.CertRequest{
		ID:     "req-2",
		Pubkey: testPubKey,
		Type:   "user",
	}
	reqStore := newApproveTestCertRequestStore(certReq)
	certStore := newApproveTestCertStore()
	auditStore := &approveTestAuditStore{}

	// Set up a waiter before rejection.
	done := make(chan error, 1)
	go func() {
		sub := certStore.GetWait("req-2")
		done <- <-sub.Phone
	}()
	time.Sleep(10 * time.Millisecond)

	req, _ := http.NewRequest(http.MethodGet, "/reject/req-2", nil)
	rr := httptest.NewRecorder()
	approveHandler(apiGetReject, "req-2", reqStore, certStore, auditStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 from reject handler, got %d", rr.Code)
	}

	if _, err := reqStore.Get("req-2"); err == nil {
		t.Error("expected cert request to be deleted after reject")
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil error on channel after reject")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out waiting for rejection signal")
	}

	if len(auditStore.entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(auditStore.entries))
	}
	if auditStore.entries[0].Decision != "rejected" {
		t.Errorf("expected decision 'rejected', got %q", auditStore.entries[0].Decision)
	}

	if len(certStore.certs) != 0 {
		t.Errorf("expected no certs after reject, got %d", len(certStore.certs))
	}
}

func TestApiGetReject_MissingRequest(t *testing.T) {
	reqStore := newApproveTestCertRequestStore(store.CertRequest{ID: "other-id", Pubkey: testPubKey})
	certStore := newApproveTestCertStore()
	auditStore := &approveTestAuditStore{}

	req, _ := http.NewRequest(http.MethodGet, "/reject/nonexistent", nil)
	rr := httptest.NewRecorder()
	approveHandler(apiGetReject, "nonexistent", reqStore, certStore, auditStore).ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "doesn't exist") {
		t.Errorf("expected 'doesn't exist' in body, got: %s", body)
	}
}
