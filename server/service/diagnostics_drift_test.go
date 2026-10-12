package service_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
)

// Test methodology: drift guard. The real response-header middleware is run
// through gin, and the headers it emits are fed to the header hygiene check
// as the edge's response, graded against the values the middleware reports
// through middleware.AppSecurityHeaders, exactly as the router wires it. An
// app response must always grade OK, so if what the middleware sends and what
// the check expects ever disagree, this fails. It lives in the external test
// package because the middleware package imports service.

// appRecordedDoer answers the probe with one recorded app response per URL.
type appRecordedDoer struct {
	url    string
	status int
	header http.Header
	body   string
}

// Do returns the recorded response for the probed URL and an error for any other.
func (d appRecordedDoer) Do(req *http.Request) (*http.Response, error) {
	if req.URL.String() != d.url {
		return nil, io.EOF
	}
	return &http.Response{
		StatusCode: d.status,
		Header:     d.header,
		Body:       io.NopCloser(strings.NewReader(d.body)),
	}, nil
}

func TestDiagnostics_HeaderHygieneOKForRealAppResponse(t *testing.T) {
	t.Parallel()

	const origin = "https://x.example"
	const hsts = "max-age=31536000; includeSubDomains"

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Same order and set as the router registers them in bootstrap.
	r.Use(middleware.NewHstsMiddleware(hsts).Add())
	r.Use(middleware.NewCspMiddleware().Add())
	r.Use(middleware.NewXFrameOptionsMiddleware().Add())
	r.Use(middleware.NewXContentTypeOptionsMiddleware().Add())
	r.Use(middleware.NewReferrerPolicyMiddleware().Add())
	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	cfg := &config.Config{}
	cfg.HTTP.PublicURL = origin
	cfg.HTTP.Hsts = hsts
	doer := appRecordedDoer{url: origin + "/healthz", status: rec.Code, header: rec.Header(), body: rec.Body.String()}

	got := service.NewDiagnosticsService(cfg, middleware.AppSecurityHeaders(), doer).Run(context.Background(), service.DiagnosticsRequest{})

	var status service.DiagnosticStatus
	for _, c := range got.Checks {
		if c.ID == "header_hygiene" {
			status = c.Status
			if c.Status != service.DiagnosticOK {
				t.Logf("findings: %v", c.Findings)
			}
		}
	}
	if status != service.DiagnosticOK {
		t.Errorf("header hygiene for the app's own response: got %q, want ok", status)
	}
}
