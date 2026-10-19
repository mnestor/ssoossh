package controller

// Test methodology: the admin key list's handler runs against the real
// key service over a real database (testdb, so make test-postgres covers
// it on Postgres too) behind the real auditor middleware. The service's
// filters and search are covered in server/service/nss_keyadmin_test.go;
// these tests cover the route, the role gate, the parameter handling and
// the wire mapping.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// adminKeysRouter mounts the NSS admin controller with env's key service
// (nil when NSS is off) behind identity and the real role middleware.
func (e *nssKeyEnv) adminKeysRouter(identity *service.Identity, enabled bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	var keys *service.NSSKeyService
	if enabled {
		keys = e.keys
	}
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewNSSAdminController(&r.RouterGroup, e.db, nil, nil, keys,
		identityMiddleware(identity), middleware.NewAdminAuthMiddleware(e.cfg).Add(),
		middleware.NewAuditorAuthMiddleware(e.cfg).Add(), func(c *gin.Context) { c.Next() })
	return r
}

// adminKeyNames lists the names of a list response's keys.
func adminKeyNames(t *testing.T, body []byte) []string {
	t.Helper()
	var page webtypes.NSSAdminKeysResponse
	decodeEnvelope(t, body, &page)
	names := []string{}
	for _, key := range page.Keys {
		names = append(names, key.Name)
	}
	return names
}

func TestNSSAdminKeys_ShouldAdmitAuditorsAndAboveWhenListed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		caller string
		want   int
	}{
		{caller: nssCallerAdmin, want: http.StatusOK},
		{caller: nssCallerSOC, want: http.StatusOK},
		{caller: nssCallerAuditor, want: http.StatusOK},
		// The fleet key role alone is not an admin area role.
		{caller: nssCallerKeyAdmin, want: http.StatusForbidden},
		{caller: nssCallerMember, want: http.StatusForbidden},
		{caller: nssCallerUnrelated, want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run("should answer "+http.StatusText(tt.want)+" when the "+tt.caller+" lists every key", func(t *testing.T) {
			t.Parallel()

			env := newNSSKeyEnv(t)
			createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
			w := doNSSRequest(env.adminKeysRouter(nssCallerNamed(t, tt.caller), true), http.MethodGet, "/admin/nss/keys")
			if w.Code != tt.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestNSSAdminKeys_ShouldAnswerNSSDisabledWhenNSSIsOff(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	w := doNSSRequest(env.adminKeysRouter(nssCallerNamed(t, nssCallerAuditor), false), http.MethodGet, "/admin/nss/keys")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), string(apitypes.ErrorCodeNSSDisabled)) {
		t.Errorf("status = %d, body = %s; want 404 nss_disabled", w.Code, w.Body.String())
	}
}

func TestNSSAdminKeys_ShouldAnswerBadRequestWhenAParameterIsWrong(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		wantBody string
	}{
		{name: "should name the problem when q is a range with too long a prefix", path: "/admin/nss/keys?q=10.0.4.0/33",
			wantBody: "10.0.4.0/33 is not a valid range: an IPv4 prefix is 0 to 32."},
		{name: "should name the problem when q is a short IPv4 address", path: "/admin/nss/keys?q=10.0.4",
			wantBody: "10.0.4 is not a valid address"},
		{name: "should refuse the cursor when it names no key", path: "/admin/nss/keys?after=nss_nothere", wantBody: "after does not name a row"},
		{name: "should refuse the limit when it is not a number", path: "/admin/nss/keys?limit=abc", wantBody: "limit must be a number"},
		{name: "should refuse offset when it is sent", path: "/admin/nss/keys?offset=0", wantBody: "offset is not supported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newNSSKeyEnv(t)
			w := doNSSRequest(env.adminKeysRouter(nssCallerNamed(t, nssCallerAuditor), true), http.MethodGet, tt.path)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("status = %d, body = %s; want 400 saying %q", w.Code, w.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestNSSAdminKeys_ShouldApplyKnownFiltersAndDropUnknownOnesWhenListed(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	live, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
	revoked, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), "team-ops")
	if _, err := env.keys.Revoke(t.Context(), nssCreatorIdentity(), revoked, ""); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	seedNSSKeyUsage(t, env.db, live, "10.0.4.7", time.Now(), 3)
	// By name: "fleet team-ops" sorts before "fleet team-web".
	everyKey := []string{"fleet team-ops", "fleet " + nssKeyGroup}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "should list every key when no filter is given", query: "", want: everyKey},
		{name: "should list every key when the status is all", query: "?status=all", want: everyKey},
		{name: "should list the revoked key when the status is revoked", query: "?status=revoked", want: []string{"fleet team-ops"}},
		{name: "should list the live key when the status is active", query: "?status=active", want: []string{"fleet " + nssKeyGroup}},
		{name: "should drop the status when it is unknown", query: "?status=gone", want: everyKey},
		{name: "should list nothing when no live key expires within 30 days", query: "?expiring_within=30", want: []string{}},
		{name: "should drop the window when it is not one the page offers", query: "?expiring_within=5", want: everyKey},
		{name: "should list the unused key when unused is 1", query: "?unused=1", want: []string{"fleet team-ops"}},
		{name: "should list the unused key when unused is true", query: "?unused=true", want: []string{"fleet team-ops"}},
		{name: "should drop unused when it is not a flag", query: "?unused=maybe", want: everyKey},
		{name: "should list nothing when no key has a recent alert", query: "?alerts=1", want: []string{}},
		{name: "should drop alerts when it is off", query: "?alerts=0", want: everyKey},
		{name: "should search the text when q is a word", query: "?q=OPS", want: []string{"fleet team-ops"}},
		{name: "should search the usage when q is an address", query: "?q=10.0.4.7", want: []string{"fleet " + nssKeyGroup}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := doNSSRequest(env.adminKeysRouter(nssCallerNamed(t, nssCallerAuditor), true), http.MethodGet, "/admin/nss/keys"+tt.query)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if got := adminKeyNames(t, w.Body.Bytes()); strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("keys = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNSSAdminKeys_ShouldMapTheKeyAndItsUseWhenAnAddressIsSearched(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	env.cfg.NSS.UsageRetention = 2160 * time.Hour
	id, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
	seedNSSKeyUsage(t, env.db, id, "10.0.4.7", time.Now(), 3)

	w := doNSSRequest(env.adminKeysRouter(nssCallerNamed(t, nssCallerSOC), true), http.MethodGet, "/admin/nss/keys?q=10.0.4.9/24&limit=10")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") && !strings.Contains(w.Body.String(), `"secret":"current"`) {
		t.Errorf("the body names a secret beyond the generation: %s", w.Body.String())
	}
	var page webtypes.NSSAdminKeysResponse
	decodeEnvelope(t, w.Body.Bytes(), &page)
	if page.AddressSearch != "10.0.4.0/24" || page.UsageRetentionSeconds != 7776000 || page.Total != 1 || page.NextCursor != nil {
		t.Fatalf("page = %+v, want the range read, the retention in seconds and one key", page)
	}
	key := page.Keys[0]
	if key.ID != id || key.GroupName != nssKeyGroup || key.Status != model.NSSKeyStatusActive || key.CurrentGeneration != 1 || key.ExpiresAt == nil {
		t.Errorf("key = %+v, want the live key", key)
	}
	if !key.Creator.Exists || key.Creator.Username != nssCreatorIdentity().Username || key.Creator.UserID == "" {
		t.Errorf("creator = %+v, want the creator's account", key.Creator)
	}
	if key.Hosts7d != 1 || key.LastSeenAt == nil {
		t.Errorf("hosts, last seen = %d, %v; want one host and a last use", key.Hosts7d, key.LastSeenAt)
	}
	if key.Matched == nil || key.Matched.Total != 1 || len(key.Matched.Addresses) != 1 ||
		key.Matched.Addresses[0].SourceIP != "10.0.4.7" || key.Matched.Addresses[0].Secret != model.NSSSecretGenerationCurrent {
		t.Errorf("matched = %+v, want 10.0.4.7 on the current secret", key.Matched)
	}
}
