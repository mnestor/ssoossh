package controller_test

// Test methodology: the real controllers behind a real httptest server,
// driven by the real internal/api client. Everything else in this package
// tests one side against a fake, which cannot catch the two sides
// disagreeing about the wire format — this can, and exists because the
// {data, error} envelope is exactly the kind of change that breaks that
// agreement silently.
//
// Lives in controller_test (not controller) so it consumes the package the
// way a caller does. server/ importing internal/ is the allowed direction;
// the reverse would invert the dependency (.claude/rules/go.md).

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/server/controller"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// stubCertRequestService is the smallest CertRequestProvider that lets the
// create-and-wait round trip run.
type stubCertRequestService struct {
	requestID string
	cert      string
}

func (s *stubCertRequestService) CreateRequest(_ context.Context, _ service.NewCertRequestParams) (string, error) {
	return s.requestID, nil
}

func (s *stubCertRequestService) Detail(_ context.Context, _ string, _ *service.Identity) (*service.RequestDetail, error) {
	return &service.RequestDetail{}, nil
}

func (s *stubCertRequestService) Approve(_ context.Context, _ string, _ *service.Identity, _ service.DecisionContext) error {
	return nil
}
func (s *stubCertRequestService) Deny(_ context.Context, _ string, _ *service.Identity, _ service.DecisionContext) error {
	return nil
}

func (s *stubCertRequestService) Wait(_ context.Context, _ string) (model.CertificateRequestStatus, string, string, error) {
	return model.CertificateRequestStatusApproved, s.cert, "", nil
}

// mockCAKeyRegistry for testing.
type mockCAKeyRegistry struct {
	keys []string
}

func (m *mockCAKeyRegistry) ActiveKeys(ctx context.Context) ([]string, error) {
	return m.keys, nil
}

// newContractServer wires the real controllers onto a real listener and
// returns a real client pointed at it.
func newContractServer(t *testing.T, certRequests service.CertRequestProvider) (*httptest.Server, api.Client) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	// Create a mock registry with a test key
	mockReg := &mockCAKeyRegistry{
		keys: []string{"ssh-ed25519 AAAA"},
	}

	caSvc, err := service.NewCAService(nil, mockReg)
	if err != nil {
		t.Fatalf("failed to build CAService: %v", err)
	}

	r := gin.New()
	apiGroup := r.Group("/api")
	controller.NewCaController(apiGroup, caSvc)

	passthrough := func(gc *gin.Context) { gc.Next() }
	controller.NewCertRequestController(apiGroup, certRequests, passthrough, passthrough, nil)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	client, err := api.NewClient(api.Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("failed to build API client: %v", err)
	}
	return srv, client
}

// TestContract_GetCA pins that the client can read a CA response the server
// actually produced, envelope and all.
func TestContract_GetCA(t *testing.T) {
	t.Parallel()

	_, client := newContractServer(t, &stubCertRequestService{})

	ca, err := client.GetCA(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ca == "" {
		t.Error("expected the client to decode a non-empty CA public key from the server's response")
	}
}

// TestContract_RequestUserCertificate covers the full create-then-wait round
// trip: an enveloped POST response the client has to unwrap to find
// events_url, followed by an enveloped SSE event it has to unwrap to find
// the certificate.
func TestContract_RequestUserCertificate(t *testing.T) {
	t.Parallel()

	const wantCert = "ssh-ed25519-cert-v01@openssh.com AAAA... contract"

	_, client := newContractServer(t, &stubCertRequestService{
		requestID: "contract-req-1",
		cert:      wantCert,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pending, err := client.CreateUserRequest(ctx, "ssh-ed25519 AAAA test", "alice", "alice-laptop", api.RequestedOptions{
		Extensions: []string{"permit-pty"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasSuffix(pending.ApprovalURL, "/approve/contract-req-1") {
		t.Errorf("got approval_url %q, want it to end in %q", pending.ApprovalURL, "/approve/contract-req-1")
	}

	result, err := client.AwaitCertificate(ctx, pending)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a certificate result")
	}
	if result.Certificate != wantCert {
		t.Errorf("got certificate %q, want %q", result.Certificate, wantCert)
	}
	if string(result.Status) != "approved" {
		t.Errorf("got status %q, want approved", result.Status)
	}
}
