// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"bytes"
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

// mockCertRequestStore is a minimal in-memory store for testing.
type mockCertRequestStore struct {
	requests map[string]store.CertRequest
}

func newMockCertRequestStore() *mockCertRequestStore {
	return &mockCertRequestStore{requests: map[string]store.CertRequest{}}
}

func (m *mockCertRequestStore) Get(id string) (*store.CertRequest, error) {
	r, ok := m.requests[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &r, nil
}

func (m *mockCertRequestStore) Create(c *store.CertRequest) error {
	if c.ID == "" {
		c.ID = "generated-id"
	}
	c.CreatedAt = time.Now()
	m.requests[c.ID] = *c
	return nil
}

func (m *mockCertRequestStore) Delete(id string) error {
	delete(m.requests, id)
	return nil
}

// signreqHandler wraps apiSignRequestPost with the session middleware so tests
// get a properly initialised session context.
func signreqHandler(sm *scs.SessionManager, reqStore store.CertRequestInterface, certStore store.CertificateInterface) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), mware.SessionContext, &mware.SessionManager{SessionManager: sm})
		ctx = context.WithValue(ctx, store.CertRequestContext, reqStore)
		ctx = context.WithValue(ctx, store.CertificateContext, certStore)
		apiSignRequestPost(w, r.WithContext(ctx))
	})
	return sm.LoadAndSave(inner)
}

const validPubKey = `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GkZH test`

func TestApiSignRequestPost_ValidKey(t *testing.T) {
	sm := scs.New()
	reqStore := newMockCertRequestStore()
	certStore := store.NewMemoryCertificatesStore()

	body, _ := json.Marshal(types.SignRequest{PublicKey: validPubKey, Type: "user", Account: ""})
	req, err := http.NewRequest(http.MethodPost, "/signreq", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	signreqHandler(sm, reqStore, certStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp types.SignRequestResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.StatusText != "success" {
		t.Errorf("expected status 'success', got %q", resp.StatusText)
	}
}

func TestApiSignRequestPost_InvalidKey(t *testing.T) {
	sm := scs.New()
	reqStore := newMockCertRequestStore()
	certStore := store.NewMemoryCertificatesStore()

	body, _ := json.Marshal(types.SignRequest{PublicKey: "not-a-real-key", Type: "user"})
	req, err := http.NewRequest(http.MethodPost, "/signreq", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	signreqHandler(sm, reqStore, certStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestApiSignRequestPost_MissingKey(t *testing.T) {
	sm := scs.New()
	reqStore := newMockCertRequestStore()
	certStore := store.NewMemoryCertificatesStore()

	body, _ := json.Marshal(types.SignRequest{PublicKey: "", Type: "user"})
	req, err := http.NewRequest(http.MethodPost, "/signreq", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	signreqHandler(sm, reqStore, certStore).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}
