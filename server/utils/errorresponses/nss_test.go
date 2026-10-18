package errorresponses

import (
	"net/http"
	"testing"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

func TestNSSErrors_ShouldCarryTheirStatusAndCodeWhenBuilt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        *NSSError
		wantStatus int
		wantCode   string
	}{
		{name: "should be a 404 nss_not_found when the name is unknown", err: NSSNotFound(), wantStatus: http.StatusNotFound, wantCode: apitypes.ErrorCodeNSSNotFound},
		{name: "should be a 400 nss_bad_request when the input is invalid", err: NSSBadRequest("bad"), wantStatus: http.StatusBadRequest, wantCode: apitypes.ErrorCodeNSSBadRequest},
		{name: "should be a 404 nss_disabled when NSS is off", err: NSSDisabled(), wantStatus: http.StatusNotFound, wantCode: apitypes.ErrorCodeNSSDisabled},
		{name: "should be a 401 nss_unauthorized when the key is refused", err: NSSUnauthorized(), wantStatus: http.StatusUnauthorized, wantCode: apitypes.ErrorCodeNSSUnauthorized},
		{name: "should be a 429 nss_rate_limited when the bucket is empty", err: NSSRateLimited(), wantStatus: http.StatusTooManyRequests, wantCode: apitypes.ErrorCodeNSSRateLimited},
		{name: "should be a 403 nss_forbidden when the address is not allowed", err: NSSForbidden(), wantStatus: http.StatusForbidden, wantCode: apitypes.ErrorCodeNSSForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.HTTPStatusCode(); got != tt.wantStatus {
				t.Errorf("HTTPStatusCode() = %d, want %d", got, tt.wantStatus)
			}
			if got := tt.err.ErrorCode(); got != tt.wantCode {
				t.Errorf("ErrorCode() = %q, want %q", got, tt.wantCode)
			}
			if tt.err.Error() == "" {
				t.Error("Error() is empty, want a caller-safe message")
			}
		})
	}
}

func TestNSSBadRequest_ShouldReturnTheReasonWhenRendered(t *testing.T) {
	t.Parallel()

	if got := NSSBadRequest("uid must be a number").Error(); got != "uid must be a number" {
		t.Errorf("Error() = %q, want the reason", got)
	}
}
