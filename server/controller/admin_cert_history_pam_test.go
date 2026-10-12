package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/test/testdb"
)

// The admin certificate list names a PAM row by the service its request
// reported, like the user's own lists, so the history query has to read it
// from the decision behind each certificate (issue #176).
func TestCertificateHistoryHandler_ShouldCarryTheReportedPAMService(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)
	db := testdb.New(t)

	now := time.Now()
	requestID := "req-pam"
	// The certificate and its decision both point at the request, and the
	// schema enforces those foreign keys.
	request := model.CertificateRequest{
		ID:        requestID,
		Type:      model.CertificateTypePAM,
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey pam@host",
		Status:    model.CertificateRequestStatusApproved,
		CreatedAt: now,
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatalf("seed request: %v", err)
	}
	certs := []model.Certificate{
		{ID: "cert-pam", Type: model.CertificateTypePAM, SerialNumber: 1, KeyID: "pam", CertificateRequestID: &requestID, IssuedAt: now, ExpiresAt: now.Add(time.Minute)},
		// No decision behind it: an orphan must still list, with no service.
		{ID: "cert-orphan", Type: model.CertificateTypeUser, SerialNumber: 2, KeyID: "orphan", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)},
	}
	if err := db.Create(&certs).Error; err != nil {
		t.Fatalf("seed certificates: %v", err)
	}
	decision := model.CertificateRequestDecision{
		ID:                   "dec-pam",
		CertificateRequestID: requestID,
		Outcome:              model.CertificateRequestDecisionApproved,
		PAMService:           "sudo",
		DecidedAt:            now,
	}
	if err := db.Create(&decision).Error; err != nil {
		t.Fatalf("seed decision: %v", err)
	}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(true),
		mockSOCAuthMiddleware(false),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: this case asserts the payload, not auditing
	)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/certificates/history", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Data struct {
			Certificates []map[string]any `json:"certificates"`
			Total        int              `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Certificates) != 2 || resp.Data.Total != 2 {
		t.Fatalf("got %d rows (total %d), want 2: the decision join must not drop or repeat a row",
			len(resp.Data.Certificates), resp.Data.Total)
	}

	byID := map[string]map[string]any{}
	for _, c := range resp.Data.Certificates {
		byID[c["id"].(string)] = c
	}

	tests := []struct {
		name    string
		id      string
		want    any
		present bool
	}{
		{name: "should carry the reported service when the PAM request reported one", id: "cert-pam", want: "sudo", present: true},
		{name: "should omit the reported service when the certificate has no decision", id: "cert-orphan", present: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, present := byID[tt.id]["reported_pam_service"]
			if present != tt.present || (tt.present && got != tt.want) {
				t.Errorf("reported_pam_service = %v (present %v), want %v (present %v)", got, present, tt.want, tt.present)
			}
		})
	}
}
