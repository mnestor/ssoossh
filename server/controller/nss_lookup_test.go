package controller

// Test methodology: the four NSS lookups run through a gin engine with
// the secret and error middleware the router installs and the real fleet
// key gate, built from a real NSSKeyService and NSSUsageService on testdb,
// in front of a real NSSLookupService (no directory refresher) over seeded
// posix_users, posix_groups and posix_group_members rows. Keys are created
// through NSSKeyService.Create, so every secret is a real one.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric/noop"
	"golang.org/x/time/rate"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/test/testdb"
)

// newNSSTestConfig is newTestConfig with NSS on, private groups off (so
// every account is served its directory gid) and the fleet key admin
// group set.
func newNSSTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := newTestConfig(t)
	cfg.Admin.NSSKeyGroup = "nss-key-admins"
	cfg.NSS = config.NSSConfig{
		Enabled:            true,
		AutoPrivateGroups:  config.AutoPrivateGroupsOff,
		TombstoneRetention: 2160 * time.Hour,
		DefaultHome:        "/home/%u",
		DisabledShell:      "/usr/sbin/nologin",
		AllowedShells:      []string{"sh", "bash", "zsh", "fish"},
		MaxGroupMembers:    2000,
		RateLimit:          20,
		RateBurst:          200,
		KeyDefaultLifetime: 8760 * time.Hour,
		KeyRotationGrace:   720 * time.Hour,
	}
	return cfg
}

// nssCreatorIdentity is the admin every test key is created by. Its users
// row is seedPlainUser's "nss-creator".
func nssCreatorIdentity() *service.Identity {
	return &service.Identity{Subject: "sub-nss-creator", Username: "nss-creator", Email: "creator@example.com", Groups: []string{"ssh-admins"}}
}

// createNSSKey creates a fleet key managed by group through the service,
// as creator, and returns its id and secret.
func createNSSKey(t *testing.T, keys *service.NSSKeyService, creator *service.Identity, group string) (string, string) {
	t.Helper()
	view, secret, err := keys.Create(t.Context(), creator, service.NSSKeyCreate{Name: "fleet " + group, GroupName: group}, "192.0.2.10")
	if err != nil {
		t.Fatalf("create fleet key: %v", err)
	}
	return view.Key.ID, secret
}

// seedNSSPosixUser writes a posix_users row, filling the id, DN and
// timestamps a test does not care about.
func seedNSSPosixUser(t *testing.T, db *gorm.DB, row model.PosixUser) {
	t.Helper()
	now := time.Now().UTC()
	if row.ID == "" {
		row.ID = "pu-" + row.Name
	}
	if row.DN == "" {
		row.DN = "uid=" + row.Name + ",ou=people,dc=example"
	}
	row.CreatedAt, row.UpdatedAt, row.LastSyncedAt = now, now, now
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed posix user %s: %v", row.Name, err)
	}
}

// seedNSSPosixGroup writes a posix_groups row.
func seedNSSPosixGroup(t *testing.T, db *gorm.DB, gid uint32, name string) {
	t.Helper()
	now := time.Now().UTC()
	group := model.PosixGroup{
		ID: "pg-" + name, GID: gid, Name: name, DN: "cn=" + name + ",ou=groups,dc=example",
		CreatedAt: now, UpdatedAt: now, LastSyncedAt: now,
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("seed posix group %s: %v", name, err)
	}
}

// seedNSSPosixMembers puts each of uids in gid's group.
func seedNSSPosixMembers(t *testing.T, db *gorm.DB, gid uint32, uids ...uint32) {
	t.Helper()
	for _, uid := range uids {
		if err := db.Create(&model.PosixGroupMember{GID: gid, UID: uid}).Error; err != nil {
			t.Fatalf("seed membership %d in %d: %v", uid, gid, err)
		}
	}
}

// nssLookupEnv is a database seeded with accounts, groups and one live
// fleet key, and the services the lookups run on.
type nssLookupEnv struct {
	db      *gorm.DB
	cfg     *config.Config
	keys    *service.NSSKeyService
	usage   *service.NSSUsageService
	lookups *service.NSSLookupService
	keyID   string
	secret  string
}

// newNSSLookupEnv seeds the directory every lookup test reads:
//
//   - alice (1500, primary 100 staff), linked to a person, with home, shell,
//     a shell choice and gecos, in devs (2000) and ops.team (2001);
//   - first.last (1501, primary 100), a shared account with none of those,
//     in devs;
//   - MixedCase (1502, primary 100);
//   - the groups staff (100, no member rows), devs, ops.team and WebOps
//     (3000).
func newNSSLookupEnv(t *testing.T) *nssLookupEnv {
	t.Helper()
	db := testdb.New(t)
	cfg := newNSSTestConfig(t)
	seedPlainUser(t, db, "nss-creator")
	seedPlainUser(t, db, "nss-alice")

	aliceUser := "nss-alice"
	seedNSSPosixUser(t, db, model.PosixUser{
		Name: "alice", UID: 1500, GID: 100, UserID: &aliceUser,
		Home: ptrTo("/home/alice"), Shell: ptrTo("/bin/bash"), ShellPref: ptrTo("zsh"), Gecos: "Alice Example",
	})
	seedNSSPosixUser(t, db, model.PosixUser{Name: "first.last", UID: 1501, GID: 100})
	seedNSSPosixUser(t, db, model.PosixUser{Name: "MixedCase", UID: 1502, GID: 100})
	seedNSSPosixGroup(t, db, 100, "staff")
	seedNSSPosixGroup(t, db, 2000, "devs")
	seedNSSPosixGroup(t, db, 2001, "ops.team")
	seedNSSPosixGroup(t, db, 3000, "WebOps")
	seedNSSPosixMembers(t, db, 2000, 1501, 1500)
	seedNSSPosixMembers(t, db, 2001, 1500)
	seedNSSPosixMembers(t, db, 3000, 1502)

	keys, err := service.NewNSSKeyService(cfg, db, nil)
	if err != nil {
		t.Fatalf("NewNSSKeyService: %v", err)
	}
	usage, err := service.NewNSSUsageService(cfg, db, keys, noop.NewMeterProvider().Meter("test"))
	if err != nil {
		t.Fatalf("NewNSSUsageService: %v", err)
	}
	env := &nssLookupEnv{db: db, cfg: cfg, keys: keys, usage: usage, lookups: service.NewNSSLookupService(cfg, db, nil)}
	env.keyID, env.secret = createNSSKey(t, keys, nssCreatorIdentity(), "")
	return env
}

// engine mounts the lookups behind a gate built from the real services,
// with the failed-key bucket large enough not to matter.
func (e *nssLookupEnv) engine(t *testing.T) *gin.Engine {
	t.Helper()
	gate, err := middleware.NewNSSFleetKeyMiddleware(middleware.NSSFleetKeyConfig{
		Enabled:     true,
		FailedLimit: rate.Every(time.Minute),
		FailedBurst: 1000,
		KeyLimit:    rate.Limit(e.cfg.NSS.RateLimit),
		KeyBurst:    e.cfg.NSS.RateBurst,
	}, e.keys, e.usage)
	if err != nil {
		t.Fatalf("NewNSSFleetKeyMiddleware: %v", err)
	}
	return newNSSLookupEngine(e.lookups, gate)
}

// newNSSLookupEngine mounts the lookup controller under /api behind the
// secret and error middleware, in the router's order.
func newNSSLookupEngine(lookups *service.NSSLookupService, gate *middleware.NSSFleetKeyMiddleware) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewNSSKeySecretMiddleware().Add())
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewNSSLookupController(r.Group("/api"), lookups, gate)
	return r
}

// serveNSSLookup sends a lookup of target with the key headers, each left
// off when empty.
func serveNSSLookup(r *gin.Engine, target, keyID, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if keyID != "" {
		req.Header.Set(apitypes.HeaderNSSKeyID, keyID)
	}
	if secret != "" {
		req.Header.Set(apitypes.HeaderNSSKeySecret, secret)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// nssEnvelopeError reads the error half of an envelope: its code and
// message.
func nssEnvelopeError(t *testing.T, body []byte) (string, string) {
	t.Helper()
	var envelope struct {
		Error     string `json:"error"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v, body: %s", err, body)
	}
	return envelope.ErrorCode, envelope.Error
}

// assertNSSError fails unless w answered status with code.
func assertNSSError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, status, w.Body.String())
	}
	if got, _ := nssEnvelopeError(t, w.Body.Bytes()); got != code {
		t.Errorf("error_code = %q, want %q", got, code)
	}
}

func TestNSSLookupController_ShouldServeTheUser(t *testing.T) {
	t.Parallel()

	env := newNSSLookupEnv(t)
	r := env.engine(t)

	alice := apitypes.NSSUserResponse{
		Name: "alice", UID: 1500, GID: 100,
		Groups: []apitypes.NSSMembership{{GID: 100, Name: "staff"}, {GID: 2000, Name: "devs"}, {GID: 2001, Name: "ops.team"}},
		Home:   ptrTo("/home/alice"), Shell: ptrTo("/bin/bash"), ShellPref: ptrTo("zsh"), Gecos: "Alice Example",
		Status: apitypes.NSSStatusActive, KeyID: env.keyID,
	}
	firstLast := apitypes.NSSUserResponse{
		Name: "first.last", UID: 1501, GID: 100,
		Groups: []apitypes.NSSMembership{{GID: 100, Name: "staff"}, {GID: 2000, Name: "devs"}},
		Home:   ptrTo("/home/first.last"), Status: apitypes.NSSStatusActive, KeyID: env.keyID,
	}
	mixed := apitypes.NSSUserResponse{
		Name: "MixedCase", UID: 1502, GID: 100,
		Groups: []apitypes.NSSMembership{{GID: 100, Name: "staff"}, {GID: 3000, Name: "WebOps"}},
		Home:   ptrTo("/home/MixedCase"), Status: apitypes.NSSStatusActive, KeyID: env.keyID,
	}

	tests := []struct {
		name   string
		target string
		want   apitypes.NSSUserResponse
	}{
		{name: "should serve the account with the key id when looked up by name", target: "/api/nss/user/name/alice", want: alice},
		{name: "should serve the same account when looked up by uid", target: "/api/nss/user/uid/1500", want: alice},
		{name: "should resolve the name when it holds a dot", target: "/api/nss/user/name/first.last", want: firstLast},
		{name: "should serve the default home and no shell when the entry has neither", target: "/api/nss/user/uid/1501", want: firstLast},
		{name: "should resolve a mixed-case name when it is sent exactly", target: "/api/nss/user/name/MixedCase", want: mixed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := serveNSSLookup(r, tc.target, env.keyID, env.secret)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			var got apitypes.NSSUserResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("user =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

func TestNSSLookupController_ShouldServeTheGroup(t *testing.T) {
	t.Parallel()

	env := newNSSLookupEnv(t)
	r := env.engine(t)

	devs := apitypes.NSSGroupResponse{Name: "devs", GID: 2000, Members: []string{"alice", "first.last"}}
	tests := []struct {
		name   string
		target string
		want   apitypes.NSSGroupResponse
	}{
		{name: "should serve the group with members by uid when looked up by name", target: "/api/nss/group/name/devs", want: devs},
		{name: "should serve the same group when looked up by gid", target: "/api/nss/group/gid/2000", want: devs},
		{name: "should resolve a group name when it holds a dot", target: "/api/nss/group/name/ops.team", want: apitypes.NSSGroupResponse{Name: "ops.team", GID: 2001, Members: []string{"alice"}}},
		{name: "should serve an empty member list when the group has no member rows", target: "/api/nss/group/gid/100", want: apitypes.NSSGroupResponse{Name: "staff", GID: 100, Members: []string{}}},
		{name: "should resolve a mixed-case group name when it is sent exactly", target: "/api/nss/group/name/WebOps", want: apitypes.NSSGroupResponse{Name: "WebOps", GID: 3000, Members: []string{"MixedCase"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := serveNSSLookup(r, tc.target, env.keyID, env.secret)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"members":[`) {
				t.Errorf("members is not a JSON array: %s", w.Body.String())
			}
			var got apitypes.NSSGroupResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("group = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNSSLookupController_ShouldAnswerNotFound(t *testing.T) {
	t.Parallel()

	env := newNSSLookupEnv(t)
	r := env.engine(t)

	tests := []struct {
		name   string
		target string
	}{
		{name: "should miss when no account has the name", target: "/api/nss/user/name/nobody"},
		{name: "should miss when no account has the uid", target: "/api/nss/user/uid/9999"},
		{name: "should miss when no group has the name", target: "/api/nss/group/name/nogroup"},
		{name: "should miss when no group has the gid", target: "/api/nss/group/gid/9999"},
		{name: "should miss when a mixed-case user name is sent in lower case", target: "/api/nss/user/name/mixedcase"},
		{name: "should miss when a mixed-case user name is sent in upper case", target: "/api/nss/user/name/MIXEDCASE"},
		{name: "should miss when a lower-case user name is sent capitalised", target: "/api/nss/user/name/Alice"},
		{name: "should miss when a mixed-case group name is sent in lower case", target: "/api/nss/group/name/webops"},
		{name: "should look a 32-byte name up rather than refuse it", target: "/api/nss/user/name/" + strings.Repeat("a", 32)},
		{name: "should look the largest uid up rather than refuse it", target: "/api/nss/user/uid/4294967295"},
		{name: "should look uid zero up rather than refuse it", target: "/api/nss/user/uid/0"},
		{name: "should look the largest gid up rather than refuse it", target: "/api/nss/group/gid/4294967295"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertNSSError(t, serveNSSLookup(r, tc.target, env.keyID, env.secret), http.StatusNotFound, apitypes.ErrorCodeNSSNotFound)
		})
	}
}

func TestNSSLookupController_ShouldRejectInvalidInput(t *testing.T) {
	t.Parallel()

	env := newNSSLookupEnv(t)
	r := env.engine(t)

	tests := []struct {
		name   string
		target string
	}{
		{name: "should refuse a user name when it is all digits", target: "/api/nss/user/name/1234"},
		{name: "should refuse a user name when it is a negative number", target: "/api/nss/user/name/-5"},
		{name: "should refuse a user name when it is a dot", target: "/api/nss/user/name/."},
		{name: "should refuse a user name when it is two dots", target: "/api/nss/user/name/.."},
		{name: "should refuse a user name when it holds an escaped slash", target: "/api/nss/user/name/a%2Fb"},
		{name: "should refuse a user name when it spans a literal extra segment", target: "/api/nss/user/name/a/b"},
		{name: "should refuse a user name when it holds a control byte", target: "/api/nss/user/name/a%01b"},
		{name: "should refuse a user name when it holds a colon", target: "/api/nss/user/name/a:b"},
		{name: "should refuse a user name when it has a leading space", target: "/api/nss/user/name/%20alice"},
		{name: "should refuse a user name when it has a trailing space", target: "/api/nss/user/name/alice%20"},
		{name: "should refuse a user name when it is not UTF-8", target: "/api/nss/user/name/%FF"},
		{name: "should refuse a user name when it is 33 bytes", target: "/api/nss/user/name/" + strings.Repeat("a", 33)},
		{name: "should refuse a user name when it is empty", target: "/api/nss/user/name/"},
		{name: "should refuse a group name when it is all digits", target: "/api/nss/group/name/42"},
		{name: "should refuse a group name when it holds an escaped slash", target: "/api/nss/group/name/a%2Fb"},
		{name: "should refuse a group name when it is empty", target: "/api/nss/group/name/"},
		{name: "should refuse a group name when it is 33 bytes", target: "/api/nss/group/name/" + strings.Repeat("g", 33)},
		{name: "should refuse a uid when it is not numeric", target: "/api/nss/user/uid/abc"},
		{name: "should refuse a uid when it is negative", target: "/api/nss/user/uid/-1"},
		{name: "should refuse a uid when it is past uint32", target: "/api/nss/user/uid/4294967296"},
		{name: "should refuse a uid when it carries a sign", target: "/api/nss/user/uid/+1"},
		{name: "should refuse a uid when it is fractional", target: "/api/nss/user/uid/1.5"},
		{name: "should refuse a uid when it is empty", target: "/api/nss/user/uid/"},
		{name: "should refuse a uid when it spans an extra segment", target: "/api/nss/user/uid/1500/x"},
		{name: "should refuse a gid when it is not numeric", target: "/api/nss/group/gid/abc"},
		{name: "should refuse a gid when it is negative", target: "/api/nss/group/gid/-1"},
		{name: "should refuse a gid when it is past uint32", target: "/api/nss/group/gid/4294967296"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertNSSError(t, serveNSSLookup(r, tc.target, env.keyID, env.secret), http.StatusBadRequest, apitypes.ErrorCodeNSSBadRequest)
		})
	}
}

func TestNSSLookupController_ShouldRefuseEveryBadKeyWithOneBody(t *testing.T) {
	t.Parallel()

	env := newNSSLookupEnv(t)
	ctx := t.Context()
	creator := nssCreatorIdentity()
	revokedID, revokedSecret := createNSSKey(t, env.keys, creator, "")
	if _, err := env.keys.Revoke(ctx, creator, revokedID, "192.0.2.10"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	expiredID, expiredSecret := createNSSKey(t, env.keys, creator, "")
	if err := env.db.Model(&model.NSSKey{}).Where("id = ?", expiredID).
		Update("expires_at", time.Now().Add(-time.Hour).UTC()).Error; err != nil {
		t.Fatalf("expire key: %v", err)
	}
	otherID, otherSecret := createNSSKey(t, env.keys, creator, "")
	r := env.engine(t)

	tests := []struct {
		name   string
		keyID  string
		secret string
	}{
		{name: "should refuse when no key headers are sent"},
		{name: "should refuse when the secret is wrong", keyID: env.keyID, secret: "ssoossh_nss_wrong"},
		{name: "should refuse when the key id is unknown", keyID: "nss_unknown1", secret: env.secret},
		{name: "should refuse when the key is revoked", keyID: revokedID, secret: revokedSecret},
		{name: "should refuse when the key is expired", keyID: expiredID, secret: expiredSecret},
		{name: "should refuse when another key's secret is sent", keyID: otherID, secret: env.secret},
		{name: "should refuse when only the key id is sent", keyID: otherID},
		{name: "should refuse when only the secret is sent", secret: otherSecret},
	}
	var first string
	for i, tc := range tests {
		w := serveNSSLookup(r, "/api/nss/user/name/alice", tc.keyID, tc.secret)
		assertNSSError(t, w, http.StatusUnauthorized, apitypes.ErrorCodeNSSUnauthorized)
		body := w.Body.String()
		if tc.secret != "" && strings.Contains(body, tc.secret) {
			t.Errorf("%s: the body carries the presented secret: %s", tc.name, body)
		}
		if i == 0 {
			first = body
			continue
		}
		if body != first {
			t.Errorf("%s: body = %s, want it byte-identical to %s", tc.name, body, first)
		}
	}
}

func TestNSSLookupController_ShouldAcceptThePreviousSecretWithinItsGrace(t *testing.T) {
	t.Parallel()

	env := newNSSLookupEnv(t)
	view, newSecret, err := env.keys.Rotate(t.Context(), nssCreatorIdentity(), env.keyID, "192.0.2.10")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	r := env.engine(t)

	for _, secret := range []string{env.secret, newSecret} {
		w := serveNSSLookup(r, "/api/nss/user/uid/1500", view.Key.ID, secret)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		var got apitypes.NSSUserResponse
		decodeEnvelope(t, w.Body.Bytes(), &got)
		if got.KeyID != env.keyID {
			t.Errorf("key_id = %q, want %q", got.KeyID, env.keyID)
		}
	}
}

func TestNSSLookupController_ShouldAnswerDisabledOnEveryRoute(t *testing.T) {
	t.Parallel()

	gate, err := middleware.NewNSSFleetKeyMiddleware(middleware.NSSFleetKeyConfig{Enabled: false}, nil, nil)
	if err != nil {
		t.Fatalf("NewNSSFleetKeyMiddleware: %v", err)
	}
	r := newNSSLookupEngine(nil, gate)

	for _, target := range []string{
		"/api/nss/user/name/alice",
		"/api/nss/user/uid/1500",
		"/api/nss/group/name/devs",
		"/api/nss/group/gid/2000",
		"/api/nss/user/name/1234",
	} {
		t.Run("should answer nss_disabled when "+target+" is asked with NSS off", func(t *testing.T) {
			t.Parallel()

			assertNSSError(t, serveNSSLookup(r, target, "nss_any", "ssoossh_nss_any"), http.StatusNotFound, apitypes.ErrorCodeNSSDisabled)
		})
	}
}

func TestNSSLookupController_ShouldAdvertiseTheKeyBucketOnAServedLookup(t *testing.T) {
	t.Parallel()

	env := newNSSLookupEnv(t)
	r := env.engine(t)

	w := serveNSSLookup(r, "/api/nss/group/gid/2000", env.keyID, env.secret)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	want := map[string]string{"RateLimit-Limit": "200", "RateLimit-Remaining": "199", "RateLimit-Reset": "0"}
	for header, value := range want {
		if got := w.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
}

func TestNSSLookupController_ShouldCountServedLookupsInTheUsage(t *testing.T) {
	t.Parallel()

	env := newNSSLookupEnv(t)
	r := env.engine(t)

	for _, target := range []string{"/api/nss/user/name/alice", "/api/nss/user/name/nobody", "/api/nss/group/gid/2000"} {
		serveNSSLookup(r, target, env.keyID, env.secret)
	}
	serveNSSLookup(r, "/api/nss/user/name/alice", env.keyID, "ssoossh_nss_wrong")
	if err := env.usage.Flush(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	var rows []model.NSSKeyUsage
	if err := env.db.Where("key_id = ?", env.keyID).Find(&rows).Error; err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("usage rows = %d, want one for the key, address, generation and day", len(rows))
	}
	got := rows[0]
	if got.Lookups != 3 || got.Misses != 1 || got.Generation != 1 || got.SourceIP != "192.0.2.1" {
		t.Errorf("usage = %d lookups, %d misses, generation %d from %q; want 3, 1, 1 from 192.0.2.1",
			got.Lookups, got.Misses, got.Generation, got.SourceIP)
	}
}

// TestNSSLookupController_ShouldLogTheKeyIDButNeverTheSecret follows the
// proposal's rule for failed verifications: the key id may be logged and
// audited, the secret never. Not parallel: it swaps the default logger,
// which every parallel test would otherwise write to as well.
func TestNSSLookupController_ShouldLogTheKeyIDButNeverTheSecret(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	env := newNSSLookupEnv(t)
	audit := service.NewAuditService(env.cfg, env.db)
	env.keys.SetAuditor(audit)
	env.usage.SetAuditor(audit)
	creator := nssCreatorIdentity()
	revokedID, revokedSecret := createNSSKey(t, env.keys, creator, "")
	if _, err := env.keys.Revoke(t.Context(), creator, revokedID, "192.0.2.10"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	r := env.engine(t)

	presented := []struct{ keyID, secret string }{
		{revokedID, revokedSecret},
		{env.keyID, "ssoossh_nss_wrong_secret"},
		{"nss_unknown1", env.secret},
	}
	for _, p := range presented {
		assertNSSError(t, serveNSSLookup(r, "/api/nss/user/name/alice", p.keyID, p.secret), http.StatusUnauthorized, apitypes.ErrorCodeNSSUnauthorized)
	}
	// Raise the queued dead key alert, as the background loop would.
	if err := env.usage.Shutdown(t.Context()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	var events []model.AuditEvent
	if err := env.db.Where("action = ?", string(service.AuditNSSKeyRevokedAttempt)).Find(&events).Error; err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].Payload, revokedID) {
		t.Errorf("revoked attempt audit = %+v, want one row naming %s", events, revokedID)
	}
	if !strings.Contains(logs.String(), revokedID) {
		t.Errorf("the logs never name the revoked key %s:\n%s", revokedID, logs.String())
	}

	var all []model.AuditEvent
	if err := env.db.Find(&all).Error; err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, secret := range []string{revokedSecret, env.secret, "ssoossh_nss_wrong_secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("a log line carries a presented secret:\n%s", logs.String())
		}
		for _, event := range all {
			if strings.Contains(event.Payload, secret) {
				t.Errorf("audit %s carries a presented secret: %s", event.Action, event.Payload)
			}
		}
	}
}
