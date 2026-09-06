package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/service"
)

// fakeDiagnostics returns a canned report and records the request it saw.
type fakeDiagnostics struct {
	report  service.DiagnosticsReport
	gotReq  service.DiagnosticsRequest
	callCnt int
}

func (f *fakeDiagnostics) Run(_ context.Context, req service.DiagnosticsRequest) service.DiagnosticsReport {
	f.callCnt++
	f.gotReq = req
	return f.report
}

func TestDiagnosticsRunHandler_ShouldMapTheReportToTheWireShape(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	fake := &fakeDiagnostics{report: service.DiagnosticsReport{
		PublicOrigin: "https://x.example",
		Checks: []service.DiagnosticCheck{
			{ID: "cors", Title: "CORS", Status: service.DiagnosticCritical, Summary: "bad", Findings: []string{"a", "b"}, Remediation: "fix it"},
			{ID: "proxy_trust", Title: "Proxy", Status: service.DiagnosticOK, Summary: "fine"},
		},
	}}

	r := gin.New()
	r.Use(errorHandlerMiddlewareForTest())
	// nil db and audit: the handler tolerates both (audit is best-effort,
	// the actor lookup is skipped without a db).
	NewDiagnosticsController(&r.RouterGroup, fake, nil, nil, passthrough, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/diagnostics/run", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if fake.callCnt != 1 {
		t.Fatalf("expected the service to run once, got %d", fake.callCnt)
	}

	var body struct {
		Data struct {
			PublicOrigin string `json:"public_origin"`
			Checks       []struct {
				ID          string   `json:"id"`
				Status      string   `json:"status"`
				Findings    []string `json:"findings"`
				Remediation string   `json:"remediation"`
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if body.Data.PublicOrigin != "https://x.example" {
		t.Errorf("got public_origin %q", body.Data.PublicOrigin)
	}
	if len(body.Data.Checks) != 2 {
		t.Fatalf("got %d checks, want 2", len(body.Data.Checks))
	}
	if body.Data.Checks[0].ID != "cors" || body.Data.Checks[0].Status != "critical" {
		t.Errorf("first check mismatch: %+v", body.Data.Checks[0])
	}
	if body.Data.Checks[0].Remediation != "fix it" {
		t.Errorf("remediation not carried: %+v", body.Data.Checks[0])
	}
}

func TestDiagnosticsRunHandler_ShouldPassTheRequestIPFacts(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	fake := &fakeDiagnostics{}

	r := gin.New()
	r.Use(errorHandlerMiddlewareForTest())
	NewDiagnosticsController(&r.RouterGroup, fake, nil, nil, passthrough, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/diagnostics/run", nil)
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	r.ServeHTTP(w, req)

	if fake.gotReq.ForwardedFor != "198.51.100.7" {
		t.Errorf("expected the X-Forwarded-For header to reach the service, got %q", fake.gotReq.ForwardedFor)
	}
	if fake.gotReq.RemoteAddr == "" {
		t.Errorf("expected the remote address to reach the service")
	}
}
