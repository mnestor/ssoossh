package controller

// Test methodology: Unit tests for HTTP controller endpoints using
// httptest.ResponseRecorder to capture responses without a real listener.
// Tests run in parallel (t.Parallel()). Uses helper functions to build
// test gin.Context objects and generate SSH test keys. Each test verifies
// one specific endpoint behavior or error case.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/service"
)

// fakeCAService is a test double for service.CAPublicKeyProvider that
// returns a canned error, used to exercise getCAHandler's error path.
type fakeCAService struct {
	err error
}

func (f *fakeCAService) GetCAPublicKey(_ context.Context) (string, error) {
	return "", f.err
}

// mockCAKeyRegistry for testing.
type mockCAKeyRegistry struct {
	keys []string
}

func (m *mockCAKeyRegistry) ActiveKeys(ctx context.Context) ([]string, error) {
	return m.keys, nil
}

// newTestCAService builds a real *service.CAService with a mock registry.
func newTestCAService(t *testing.T) *service.CAService {
	t.Helper()

	mockReg := &mockCAKeyRegistry{
		keys: []string{"ssh-ed25519 AAAA"},
	}

	svc, err := service.NewCAService(nil, mockReg)
	if err != nil {
		t.Fatalf("failed to build CAService: %v", err)
	}
	return svc
}

func TestGetCAHandler_ShouldReturn200WithCAPublicKey(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := newTestCAService(t)

	r := gin.New()
	NewCaController(&r.RouterGroup, svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ca", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var body struct {
		CA string `json:"ca"`
	}
	decodeEnvelope(t, w.Body.Bytes(), &body)
	if body.CA == "" {
		t.Error("expected non-empty ca public key in response body")
	}

	wantPub, err := svc.GetCAPublicKey(req.Context())
	if err != nil {
		t.Fatalf("unexpected error from GetCAPublicKey: %v", err)
	}
	if body.CA != wantPub {
		t.Errorf("got ca %q, want %q", body.CA, wantPub)
	}
}

func TestGetCAHandler_ShouldRegisterErrorWhenServiceFails(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCAService{err: errors.New("simulated CA lookup failure")}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCaController(&r.RouterGroup, svc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ca", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when the service fails, got %d", gotErrors)
	}
}

func TestNewCaController_ShouldRegisterGetOnCaPath(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := newTestCAService(t)

	r := gin.New()
	NewCaController(&r.RouterGroup, svc)

	w := httptest.NewRecorder()
	// POST should not be registered; only GET /ca is.
	req := httptest.NewRequest(http.MethodPost, "/ca", nil)
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("expected POST /ca to not be handled the same as GET /ca")
	}
}
