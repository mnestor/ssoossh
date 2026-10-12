package controller

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// levelRecorder is an slog.Handler that keeps the level of every record it
// is handed, so a test can say what was logged at ERROR.
type levelRecorder struct {
	mu     sync.Mutex
	levels []slog.Level
}

// Enabled accepts every level, so nothing is filtered before Handle.
func (r *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }

// Handle records the record's level.
func (r *levelRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.levels = append(r.levels, rec.Level)
	return nil
}

// WithAttrs returns the same recorder: attributes do not matter here.
func (r *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

// WithGroup returns the same recorder: groups do not matter here.
func (r *levelRecorder) WithGroup(string) slog.Handler { return r }

// errorsLogged counts the records at ERROR or above.
func (r *levelRecorder) errorsLogged() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, level := range r.levels {
		if level >= slog.LevelError {
			n++
		}
	}
	return n
}

// TestDecisionHandlers_ShouldAnswerARequestNoLongerPendingWithItsOwnStatus
// sends the service's refusals through the real error handler: a request
// already decided is 409, an expired one 410, and a service account the
// approver does not hold 403, each with its reason in the body and none
// logged as an unhandled error.
//
// Not parallel: it swaps slog's default logger to see what the error
// handler logs. Top-level tests that call t.Parallel do not start until
// this one has finished, so nothing else logs through the swapped handler.
func TestDecisionHandlers_ShouldAnswerARequestNoLongerPendingWithItsOwnStatus(t *testing.T) {
	recorder := &levelRecorder{}
	prev := slog.Default()
	slog.SetDefault(slog.New(recorder))
	t.Cleanup(func() { slog.SetDefault(prev) })

	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		path       string
		svc        *fakeCertRequestService
		wantStatus int
		wantCode   string
		wantError  string
	}{
		{
			name:       "should answer 409 when approving a request already denied",
			path:       "/certs/requests/req-1/approve",
			svc:        &fakeCertRequestService{approveErr: &errorresponses.ConflictError{Reason: "this certificate request was already denied"}},
			wantStatus: http.StatusConflict,
			wantCode:   apitypes.ErrorCodeConflict,
			wantError:  "this certificate request was already denied",
		},
		{
			name:       "should answer 410 when approving a request that expired",
			path:       "/certs/requests/req-1/approve",
			svc:        &fakeCertRequestService{approveErr: &errorresponses.ExpiredError{Resource: "certificate request"}},
			wantStatus: http.StatusGone,
			wantCode:   apitypes.ErrorCodeUnavailable,
			wantError:  "this certificate request has expired",
		},
		{
			name:       "should answer 403 when approving for a service account the approver does not hold",
			path:       "/certs/requests/req-1/approve",
			svc:        &fakeCertRequestService{approveErr: &errorresponses.ForbiddenError{Reason: `identity is not associated with service account "svc-payroll"`}},
			wantStatus: http.StatusForbidden,
			wantCode:   apitypes.ErrorCodeForbidden,
			wantError:  `identity is not associated with service account "svc-payroll"`,
		},
		{
			name:       "should answer 409 when denying a request already approved",
			path:       "/certs/requests/req-1/deny",
			svc:        &fakeCertRequestService{denyErr: &errorresponses.ConflictError{Reason: "this certificate request was already approved"}},
			wantStatus: http.StatusConflict,
			wantCode:   apitypes.ErrorCodeConflict,
			wantError:  "this certificate request was already approved",
		},
		{
			name:       "should answer 410 when denying a request that expired",
			path:       "/certs/requests/req-1/deny",
			svc:        &fakeCertRequestService{denyErr: &errorresponses.ExpiredError{Resource: "certificate request"}},
			wantStatus: http.StatusGone,
			wantCode:   apitypes.ErrorCodeUnavailable,
			wantError:  "this certificate request has expired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.NewErrorHandlerMiddleware().Add())
			NewCertRequestController(&r.RouterGroup, tt.svc, withIdentity("sub-alice"), passthrough, nil)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tt.path, nil))

			if w.Code != tt.wantStatus {
				t.Fatalf("got status %d, want %d, body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			var body struct {
				Error     string `json:"error"`
				ErrorCode string `json:"error_code"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("failed to decode the error body %q: %v", w.Body.String(), err)
			}
			if body.Error != tt.wantError {
				t.Errorf("got error %q, want %q", body.Error, tt.wantError)
			}
			if body.ErrorCode != tt.wantCode {
				t.Errorf("got error_code %q, want %q", body.ErrorCode, tt.wantCode)
			}
		})
	}

	if n := recorder.errorsLogged(); n != 0 {
		t.Errorf("got %d records logged at ERROR, want none: a refusal is not a server fault", n)
	}
}
