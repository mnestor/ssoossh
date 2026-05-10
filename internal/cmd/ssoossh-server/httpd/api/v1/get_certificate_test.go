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

// mockCertificateStore satisfies store.CertificateInterface for tests.
type mockCertificateStore struct {
	certs map[string]store.Certificate
	subs  map[string]chan error
}

func newMockCertificateStore() *mockCertificateStore {
	return &mockCertificateStore{
		certs: map[string]store.Certificate{},
		subs:  map[string]chan error{},
	}
}

func (m *mockCertificateStore) Get(id string) (*store.Certificate, error) {
	c, ok := m.certs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &c, nil
}

func (m *mockCertificateStore) Create(c *store.Certificate) error {
	c.CreatedAt = time.Now()
	m.certs[c.ID] = *c
	if ch, ok := m.subs[c.ID]; ok {
		ch <- nil
	}
	return nil
}

func (m *mockCertificateStore) Delete(id string) error {
	delete(m.certs, id)
	return nil
}

func (m *mockCertificateStore) GetWait(id string) *store.Subscriber {
	ch := make(chan error, 1)
	m.subs[id] = ch
	return &store.Subscriber{ID: id, Phone: ch}
}

func (m *mockCertificateStore) Reject(id string) error {
	if ch, ok := m.subs[id]; ok {
		ch <- errors.New("rejected")
	}
	return nil
}

// certHandler wraps apiGetCertificate with session middleware and injects the
// given signreq ID and cert store into the context.
func certHandler(sm *scs.SessionManager, signreqID string, certStore store.CertificateInterface) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), mware.SessionContext, &mware.SessionManager{SessionManager: sm})
		ctx = context.WithValue(ctx, store.CertificateContext, certStore)
		sm.Put(ctx, "signreq", signreqID)
		apiGetCertificate(w, r.WithContext(ctx))
	})
	return sm.LoadAndSave(inner)
}

func TestApiGetCertificate_Approved(t *testing.T) {
	sm := scs.New()
	certStore := newMockCertificateStore()

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = certStore.Create(&store.Certificate{ID: "test-cert-id", Certificate: "ssh-rsa-cert-v01@openssh.com AAAA..."})
	}()

	req, err := http.NewRequest(http.MethodGet, "/certificate", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	certHandler(sm, "test-cert-id", certStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp types.CertificateRequestResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.StatusText != "success" {
		t.Errorf("expected status 'success', got %q", resp.StatusText)
	}
}

func TestApiGetCertificate_Rejected(t *testing.T) {
	sm := scs.New()
	certStore := newMockCertificateStore()

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = certStore.Reject("reject-cert-id")
	}()

	req, err := http.NewRequest(http.MethodGet, "/certificate", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	certHandler(sm, "reject-cert-id", certStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestApiGetCertificate_Timeout(t *testing.T) {
	sm := scs.New()
	certStore := newMockCertificateStore()

	req, err := http.NewRequest(http.MethodGet, "/certificate", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Pre-cancel context to force an immediate timeout inside the handler.
	cancelCtx, cancel := context.WithTimeout(req.Context(), 1*time.Millisecond)
	defer cancel()
	req = req.WithContext(cancelCtx)

	rr := httptest.NewRecorder()
	certHandler(sm, "timeout-id", certStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestTimeout {
		t.Errorf("expected 408, got %d: %s", rr.Code, rr.Body.String())
	}
}
