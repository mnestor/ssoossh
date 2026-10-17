package apitypes

import "testing"

// TestNSSWireStrings pins every NSS constant's spelling. These strings are
// read by a daemon that may be a release behind or ahead of the server, so a
// rename compiles on both sides and breaks the fleet; the goldens pin the
// JSON shapes, and this pins the strings that travel beside them.
func TestNSSWireStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "should name the key id header", got: HeaderNSSKeyID, want: "X-Ssoossh-Key-Id"},
		{name: "should name the key secret header", got: HeaderNSSKeySecret, want: "X-Ssoossh-Key-Secret"},
		{name: "should spell the not-found code", got: ErrorCodeNSSNotFound, want: "nss_not_found"},
		{name: "should spell the bad-request code", got: ErrorCodeNSSBadRequest, want: "nss_bad_request"},
		{name: "should spell the disabled code", got: ErrorCodeNSSDisabled, want: "nss_disabled"},
		{name: "should spell the unauthorized code", got: ErrorCodeNSSUnauthorized, want: "nss_unauthorized"},
		{name: "should spell the rate-limited code", got: ErrorCodeNSSRateLimited, want: "nss_rate_limited"},
		{name: "should spell the forbidden code", got: ErrorCodeNSSForbidden, want: "nss_forbidden"},
		{name: "should spell the active status", got: NSSStatusActive, want: "active"},
		{name: "should spell the disabled status", got: NSSStatusDisabled, want: "disabled"},
		{name: "should spell the deleted status", got: NSSStatusDeleted, want: "deleted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
