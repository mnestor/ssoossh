package controller

// Test methodology: POST /certs/console through a gin engine wired to the
// real CertRequestService and AuditService over an in-memory database, so
// the source address in the audit row is the one gin resolved from the
// connection and http.trusted_proxies. The console type only accepts
// 10.20.0.0/16. Rows are read back from the audit table.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// refusedNetworkRouter builds the console create route over a real service
// whose console type accepts only 10.20.0.0/16, trusting the given proxies.
func refusedNetworkRouter(t *testing.T, trustedProxies []string) (*gin.Engine, *gorm.DB) {
	t.Helper()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.CertificateRequestDecision{}); err != nil {
		t.Fatalf("migrate certificate request tables: %v", err)
	}
	cfg := &config.Config{CertOptions: config.CertificateOptions{
		ClientTimeout: 5 * time.Minute,
		Console: config.CertOptionsConsole{
			ClientTimeout:   2 * time.Minute,
			ValidDuration:   30 * time.Second,
			AllowedNetworks: []string{"10.20.0.0/16"},
		},
	}}

	channel := gochannel.NewGoChannel(gochannel.Config{}, watermill.NopLogger{})
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("close gochannel: %v", err)
		}
	})
	svc, err := service.NewCertRequestService(cfg, db, channel, channel)
	if err != nil {
		t.Fatalf("NewCertRequestService: %v", err)
	}
	svc.SetAuditor(service.NewAuditService(cfg, db))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)
	return r, db
}

// consoleCreate is a console create request from peer, with the given extra
// headers.
func consoleCreate(peer string, headers map[string]string) *http.Request {
	body := `{"public_key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey console@web01","username":"root","hostname":"web01"}`
	req := httptest.NewRequest(http.MethodPost, "/certs/console", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestCreateConsoleRequestHandler_ShouldRefuseOutsideTheAllowedNetworks(t *testing.T) {
	t.Parallel()

	r, db := refusedNetworkRouter(t, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, consoleCreate("203.0.113.9:4242", nil))

	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body.String(), err)
	}
	var requests int64
	if err := db.Model(&model.CertificateRequest{}).Count(&requests).Error; err != nil {
		t.Fatalf("count certificate requests: %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "should answer 403", got: w.Code, want: http.StatusForbidden},
		{name: "should keep the refusal text that names no network", got: body.Error, want: "console certificate requests are not accepted from this network"},
		{name: "should write one refusal row", got: len(auditEventsWithAction(t, db, string(service.AuditCertRequestRefusedNetwork))), want: 1},
		{name: "should persist no certificate request", got: requests, want: int64(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

// The recorded source is the address the server observed: the peer, or a
// forwarded address only when the peer is a trusted proxy.
func TestCreateConsoleRequestHandler_ShouldAuditTheObservedSourceAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		trusted   []string
		peer      string
		forwarded string
		wantCode  int
		wantRows  int
		wantIP    string
	}{
		{
			name:     "should record the peer address when nothing is forwarded",
			peer:     "203.0.113.9:4242",
			wantCode: http.StatusForbidden, wantRows: 1, wantIP: "203.0.113.9",
		},
		{
			name:      "should ignore X-Forwarded-For from an untrusted peer when it names an allowed address",
			peer:      "203.0.113.9:4242",
			forwarded: "10.20.3.4",
			wantCode:  http.StatusForbidden, wantRows: 1, wantIP: "203.0.113.9",
		},
		{
			name:      "should ignore X-Forwarded-For from an untrusted peer when it names another refused address",
			peer:      "203.0.113.9:4242",
			forwarded: "198.51.100.7",
			wantCode:  http.StatusForbidden, wantRows: 1, wantIP: "203.0.113.9",
		},
		{
			name:      "should record the forwarded address when the peer is a trusted proxy",
			trusted:   []string{"10.20.0.1/32"},
			peer:      "10.20.0.1:4242",
			forwarded: "198.51.100.7",
			wantCode:  http.StatusForbidden, wantRows: 1, wantIP: "198.51.100.7",
		},
		{
			name:     "should write no refusal row when the peer is inside the allowed network",
			peer:     "10.20.3.4:4242",
			wantCode: http.StatusOK, wantRows: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, db := refusedNetworkRouter(t, tt.trusted)
			headers := map[string]string{}
			if tt.forwarded != "" {
				headers["X-Forwarded-For"] = tt.forwarded
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, consoleCreate(tt.peer, headers))

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d, body: %s", w.Code, tt.wantCode, w.Body.String())
			}
			rows := auditEventsWithAction(t, db, string(service.AuditCertRequestRefusedNetwork))
			if len(rows) != tt.wantRows {
				t.Fatalf("refusal rows = %d, want %d", len(rows), tt.wantRows)
			}
			if tt.wantRows == 0 {
				return
			}
			if got := decodeAuditPayload(t, rows[0]).Detail["source_ip"]; got != tt.wantIP {
				t.Errorf("source_ip = %v, want %s", got, tt.wantIP)
			}
			// The indexed copy the admin feed filters on holds the same
			// observed address, never the untrusted forwarded one.
			if rows[0].SourceIP != tt.wantIP {
				t.Errorf("source_ip column = %q, want %s", rows[0].SourceIP, tt.wantIP)
			}
		})
	}
}

// Audit records never capture Cookie or Authorization.
func TestCreateConsoleRequestHandler_ShouldNotAuditCredentialHeadersOnARefusal(t *testing.T) {
	t.Parallel()

	r, db := refusedNetworkRouter(t, nil)
	r.ServeHTTP(httptest.NewRecorder(), consoleCreate("203.0.113.9:4242", map[string]string{
		"Cookie":        "session=cookie-secret-value",
		"Authorization": "Bearer auth-secret-value",
	}))

	rows := loadAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	for _, secret := range []string{"cookie-secret-value", "auth-secret-value"} {
		if strings.Contains(rows[0].Payload, secret) {
			t.Errorf("audit payload carries %q: %s", secret, rows[0].Payload)
		}
	}
}
