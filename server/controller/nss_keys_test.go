package controller

// Test methodology: the session NSS routes run through a gin engine with
// the error middleware and the real NSSKeyService and NSSLookupService on
// testdb. identityMiddleware stands in for SessionAuthMiddleware and CSRF
// passes through. Every caller has a users row (subject = identity.Subject),
// because the mutations resolve users.id by subject. The role matrix builds
// a fresh database and key per cell, so a mutation in one never shapes
// another.

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
	"github.com/mnestor/ssoossh/test/testdb"
)

// The callers the role matrix is written for.
const (
	nssCallerAdmin     = "admin"
	nssCallerKeyAdmin  = "key admin"
	nssCallerSOC       = "SOC"
	nssCallerAuditor   = "auditor"
	nssCallerMember    = "group member"
	nssCallerUnrelated = "unrelated user"
)

// nssKeyGroup is the group that manages the matrix's key.
const nssKeyGroup = "team-web"

// nssCaller is one caller of the key routes.
type nssCaller struct {
	name     string
	identity *service.Identity
}

// nssCallers is one caller per role: admin.require_group,
// admin.nss_key_group, SOC, auditor, a member of the key's group and a user
// with none of those. Each identity's users row is seedPlainUser's
// Username.
func nssCallers() []nssCaller {
	caller := func(name, id string, groups ...string) nssCaller {
		return nssCaller{name: name, identity: &service.Identity{
			Subject: "sub-" + id, Username: id, Email: id + "@example.com", Groups: groups,
		}}
	}
	return []nssCaller{
		caller(nssCallerAdmin, "nss-admin", "ssh-admins"),
		caller(nssCallerKeyAdmin, "nss-keyadmin", "nss-key-admins"),
		caller(nssCallerSOC, "nss-soc", "ssh-soc"),
		caller(nssCallerAuditor, "nss-auditor", "ssh-auditors"),
		caller(nssCallerMember, "nss-member", nssKeyGroup),
		caller(nssCallerUnrelated, "nss-plain", "ssh-users"),
	}
}

// nssCallerNamed returns the caller called name.
func nssCallerNamed(t *testing.T, name string) *service.Identity {
	t.Helper()
	for _, caller := range nssCallers() {
		if caller.name == name {
			return caller.identity
		}
	}
	t.Fatalf("no caller named %q", name)
	return nil
}

// nssKeyEnv is a database with every caller's users row, and the services
// the key routes run on, audited as in production.
type nssKeyEnv struct {
	db      *gorm.DB
	cfg     *config.Config
	keys    *service.NSSKeyService
	lookups *service.NSSLookupService
}

// newNSSKeyEnv builds the environment on a fresh database.
func newNSSKeyEnv(t *testing.T) *nssKeyEnv {
	t.Helper()
	db := testdb.New(t)
	cfg := newNSSTestConfig(t)
	seedPlainUser(t, db, nssCreatorIdentity().Username)
	for _, caller := range nssCallers() {
		seedPlainUser(t, db, caller.identity.Username)
	}
	keys, err := service.NewNSSKeyService(cfg, db, nil)
	if err != nil {
		t.Fatalf("NewNSSKeyService: %v", err)
	}
	keys.SetAuditor(service.NewAuditService(cfg, db))
	return &nssKeyEnv{db: db, cfg: cfg, keys: keys, lookups: service.NewNSSLookupService(cfg, db, nil)}
}

// router mounts the session NSS routes for identity.
func (e *nssKeyEnv) router(identity *service.Identity) *gin.Engine {
	return newNSSKeyRouter(e.keys, e.lookups, identityMiddleware(identity))
}

// newNSSKeyRouter mounts NewNSSController at the root behind the error
// middleware, with sessionAuth in the session middleware's place and CSRF
// passing through.
func newNSSKeyRouter(keys *service.NSSKeyService, lookups *service.NSSLookupService, sessionAuth gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewNSSController(&r.RouterGroup, keys, lookups, sessionAuth, func(g *gin.Context) { g.Next() })
	return r
}

// serveNSSKeys sends method to target, with body as JSON when it is not
// empty.
func serveNSSKeys(r *gin.Engine, method, target, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// nssFuture is an expires_at a day ahead, as JSON.
func nssFuture() string {
	return time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
}

// nssWant lays out one route's expected status per caller.
func nssWant(admin, keyAdmin, soc, auditor, member, unrelated int) map[string]int {
	return map[string]int{
		nssCallerAdmin: admin, nssCallerKeyAdmin: keyAdmin, nssCallerSOC: soc,
		nssCallerAuditor: auditor, nssCallerMember: member, nssCallerUnrelated: unrelated,
	}
}

// nssRotateFirst rotates the key as its creator, so it has a previous
// secret to end.
func nssRotateFirst(t *testing.T, e *nssKeyEnv, id string) {
	t.Helper()
	if _, _, err := e.keys.Rotate(t.Context(), nssCreatorIdentity(), id, "192.0.2.10"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
}

func TestNSSKeyController_RoleMatrix(t *testing.T) {
	t.Parallel()

	const ok, forbidden, notFound = http.StatusOK, http.StatusForbidden, http.StatusNotFound
	view := nssWant(ok, ok, ok, ok, ok, notFound)
	everyone := nssWant(ok, ok, ok, ok, ok, ok)
	create := nssWant(ok, ok, forbidden, forbidden, forbidden, forbidden)
	edit := nssWant(ok, ok, forbidden, forbidden, ok, notFound)
	assign := nssWant(ok, ok, forbidden, forbidden, forbidden, notFound)
	revoke := nssWant(ok, ok, ok, forbidden, ok, notFound)

	routes := []struct {
		name    string
		method  string
		path    string
		body    string
		prepare func(*testing.T, *nssKeyEnv, string)
		want    map[string]int
	}{
		{name: "list the keys", method: http.MethodGet, path: "/nss/keys", want: everyone},
		{name: "create a key", method: http.MethodPost, path: "/nss/keys", body: `{"name":"new fleet","group_name":"team-web"}`, want: create},
		{name: "read a key", method: http.MethodGet, path: "/nss/keys/{id}", want: view},
		{name: "edit a key", method: http.MethodPatch, path: "/nss/keys/{id}",
			body: `{"description":"changed","expires_at":"` + nssFuture() + `","notification_email":"ops@example.com"}`, want: edit},
		{name: "reassign a key's group", method: http.MethodPatch, path: "/nss/keys/{id}", body: `{"group_name":"team-ops"}`, want: assign},
		{name: "edit and reassign a key at once", method: http.MethodPatch, path: "/nss/keys/{id}", body: `{"description":"changed","group_name":"team-ops"}`, want: assign},
		{name: "rotate a key", method: http.MethodPost, path: "/nss/keys/{id}/rotate", want: edit},
		{name: "end a key's previous secret", method: http.MethodDelete, path: "/nss/keys/{id}/previous", prepare: nssRotateFirst, want: edit},
		{name: "revoke a key", method: http.MethodPost, path: "/nss/keys/{id}/revoke", want: revoke},
		{name: "read a key's usage", method: http.MethodGet, path: "/nss/keys/{id}/usage", want: view},
		{name: "read a key's rotation report", method: http.MethodGet, path: "/nss/keys/{id}/rotation-report", want: view},
		{name: "download a key's rotation report", method: http.MethodGet, path: "/nss/keys/{id}/rotation-report?format=csv", want: view},
		{name: "list the allowed shells", method: http.MethodGet, path: "/nss/shells", want: everyone},
	}
	for _, route := range routes {
		for _, caller := range nssCallers() {
			want := route.want[caller.name]
			t.Run("should answer "+http.StatusText(want)+" when the "+caller.name+" tries to "+route.name, func(t *testing.T) {
				t.Parallel()

				env := newNSSKeyEnv(t)
				id, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
				if route.prepare != nil {
					route.prepare(t, env, id)
				}

				w := serveNSSKeys(env.router(caller.identity), route.method, strings.ReplaceAll(route.path, "{id}", id), route.body)

				if w.Code != want {
					t.Errorf("status = %d, want %d, body = %s", w.Code, want, w.Body.String())
				}
			})
		}
	}
}

func TestNSSKeyController_ShouldListTheKeysEachCallerMayView(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	managed, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
	unmanaged, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), "")
	other, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), "team-ops")
	all := []string{managed, unmanaged, other}

	tests := []struct {
		caller        string
		wantIDs       []string
		wantCanCreate bool
	}{
		{caller: nssCallerAdmin, wantIDs: all, wantCanCreate: true},
		{caller: nssCallerKeyAdmin, wantIDs: all, wantCanCreate: true},
		{caller: nssCallerSOC, wantIDs: all},
		{caller: nssCallerAuditor, wantIDs: all},
		{caller: nssCallerMember, wantIDs: []string{managed}},
		{caller: nssCallerUnrelated, wantIDs: []string{}},
	}
	for _, tc := range tests {
		t.Run("should list what the "+tc.caller+" may view", func(t *testing.T) {
			t.Parallel()

			w := serveNSSKeys(env.router(nssCallerNamed(t, tc.caller)), http.MethodGet, "/nss/keys", "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"keys":[`) {
				t.Errorf("keys is not a JSON array: %s", w.Body.String())
			}
			var got webtypes.NSSKeyListResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			gotIDs := []string{}
			for _, key := range got.Keys {
				gotIDs = append(gotIDs, key.ID)
			}
			if !sameNSSIDs(gotIDs, tc.wantIDs) {
				t.Errorf("listed %v, want %v", gotIDs, tc.wantIDs)
			}
			if got.CanCreate != tc.wantCanCreate {
				t.Errorf("can_create = %v, want %v", got.CanCreate, tc.wantCanCreate)
			}
		})
	}
}

// sameNSSIDs reports whether a and b hold the same ids in any order.
func sameNSSIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, id := range a {
		seen[id]++
	}
	for _, id := range b {
		seen[id]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestNSSKeyController_ShouldReportEachCallersPermissions(t *testing.T) {
	t.Parallel()

	full := []model.NSSKeyPermission{"view", "rotate", "revoke", "edit", "assign_group"}
	tests := []struct {
		caller      string
		revoked     bool
		wantPerms   []model.NSSKeyPermission
		wantVisible bool
	}{
		{caller: nssCallerAdmin, wantPerms: full, wantVisible: true},
		{caller: nssCallerKeyAdmin, wantPerms: full, wantVisible: true},
		{caller: nssCallerSOC, wantPerms: []model.NSSKeyPermission{"view", "revoke"}, wantVisible: true},
		{caller: nssCallerAuditor, wantPerms: []model.NSSKeyPermission{"view"}, wantVisible: true},
		{caller: nssCallerMember, wantPerms: []model.NSSKeyPermission{"view", "rotate", "revoke", "edit"}, wantVisible: true},
		{caller: nssCallerUnrelated, wantVisible: false},
		{caller: nssCallerAdmin, revoked: true, wantPerms: []model.NSSKeyPermission{"view"}, wantVisible: true},
		{caller: nssCallerKeyAdmin, revoked: true, wantPerms: []model.NSSKeyPermission{"view"}, wantVisible: true},
		{caller: nssCallerSOC, revoked: true, wantPerms: []model.NSSKeyPermission{"view"}, wantVisible: true},
		{caller: nssCallerMember, revoked: true, wantPerms: []model.NSSKeyPermission{"view"}, wantVisible: true},
	}
	for _, tc := range tests {
		state := "a live key"
		if tc.revoked {
			state = "a revoked key"
		}
		t.Run("should give the "+tc.caller+" their permissions when they read "+state, func(t *testing.T) {
			t.Parallel()

			env := newNSSKeyEnv(t)
			id, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
			if tc.revoked {
				if _, err := env.keys.Revoke(t.Context(), nssCreatorIdentity(), id, "192.0.2.10"); err != nil {
					t.Fatalf("revoke: %v", err)
				}
			}
			r := env.router(nssCallerNamed(t, tc.caller))

			w := serveNSSKeys(r, http.MethodGet, "/nss/keys/"+id, "")
			if !tc.wantVisible {
				if w.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 404 for a key the caller may not view", w.Code)
				}
				return
			}
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			var got webtypes.NSSKeyResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if !reflect.DeepEqual(got.Permissions, tc.wantPerms) {
				t.Errorf("get permissions = %v, want %v", got.Permissions, tc.wantPerms)
			}

			var list webtypes.NSSKeyListResponse
			decodeEnvelope(t, serveNSSKeys(r, http.MethodGet, "/nss/keys", "").Body.Bytes(), &list)
			if len(list.Keys) != 1 || !reflect.DeepEqual(list.Keys[0].Permissions, tc.wantPerms) {
				t.Errorf("list = %+v, want the one key with permissions %v", list.Keys, tc.wantPerms)
			}
		})
	}
}

func TestNSSKeyController_ShouldReturnTheSecretOnlyOnCreateAndRotate(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	caller := nssCallerNamed(t, nssCallerKeyAdmin)
	r := env.router(caller)
	ctx := t.Context()

	// Create: the key and its secret, once.
	w := serveNSSKeys(r, http.MethodPost, "/nss/keys", `{"name":"web fleet","group_name":"team-web"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}
	var created webtypes.NSSKeySecretResponse
	decodeEnvelope(t, w.Body.Bytes(), &created)
	first := created.Secret
	if !strings.HasPrefix(first, "ssoossh_nss_") {
		t.Errorf("secret = %q, want the ssoossh_nss_ prefix", first)
	}
	key := created.Key
	if !strings.HasPrefix(key.ID, "nss_") || len(key.ID) != len("nss_")+8 {
		t.Errorf("id = %q, want nss_ and 8 characters", key.ID)
	}
	if key.CurrentGeneration != 1 || key.Status != model.NSSKeyStatusActive || key.GroupName != "team-web" {
		t.Errorf("created key = generation %d, %s, group %q; want 1, active, team-web", key.CurrentGeneration, key.Status, key.GroupName)
	}
	if key.CreatedByUsername != caller.Username || key.NotificationEmail != caller.Email {
		t.Errorf("created by %q mailing %q, want %q mailing %q", key.CreatedByUsername, key.NotificationEmail, caller.Username, caller.Email)
	}
	if want := time.Now().Add(env.cfg.NSS.KeyDefaultLifetime); key.ExpiresAt.Sub(want).Abs() > time.Minute {
		t.Errorf("expires_at = %v, want about %v", key.ExpiresAt, want)
	}
	if verdict, err := env.keys.VerifyFleetKey(ctx, key.ID, first); err != nil || !verdict.OK || verdict.Generation != 1 {
		t.Errorf("verify the created secret = %+v, %v; want accepted at generation 1", verdict, err)
	}

	// Reads and edits never carry it.
	for _, read := range []struct{ method, target, body string }{
		{http.MethodGet, "/nss/keys/" + key.ID, ""},
		{http.MethodGet, "/nss/keys", ""},
		{http.MethodPatch, "/nss/keys/" + key.ID, `{"description":"web servers"}`},
	} {
		w := serveNSSKeys(r, read.method, read.target, read.body)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, body = %s", read.method, read.target, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"secret"`) || strings.Contains(w.Body.String(), first) {
			t.Errorf("%s %s carries the secret: %s", read.method, read.target, w.Body.String())
		}
	}

	// Rotate: a new secret, once; the old one becomes the previous one.
	w = serveNSSKeys(r, http.MethodPost, "/nss/keys/"+key.ID+"/rotate", "")
	if w.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body = %s", w.Code, w.Body.String())
	}
	var rotated webtypes.NSSKeySecretResponse
	decodeEnvelope(t, w.Body.Bytes(), &rotated)
	second := rotated.Secret
	if !strings.HasPrefix(second, "ssoossh_nss_") || second == first {
		t.Errorf("rotated secret = %q, want a new ssoossh_nss_ secret", second)
	}
	if rotated.Key.CurrentGeneration != 2 {
		t.Errorf("current_generation = %d, want 2", rotated.Key.CurrentGeneration)
	}
	if rotated.Key.RotatedAt == nil || rotated.Key.PreviousExpiresAt == nil {
		t.Fatalf("rotated_at = %v, previous_expires_at = %v, want both set", rotated.Key.RotatedAt, rotated.Key.PreviousExpiresAt)
	}
	if want := time.Now().Add(env.cfg.NSS.KeyRotationGrace); rotated.Key.PreviousExpiresAt.Sub(want).Abs() > time.Minute {
		t.Errorf("previous_expires_at = %v, want about %v", *rotated.Key.PreviousExpiresAt, want)
	}
	for secret, generation := range map[string]int{first: 1, second: 2} {
		if verdict, err := env.keys.VerifyFleetKey(ctx, key.ID, secret); err != nil || !verdict.OK || verdict.Generation != generation {
			t.Errorf("verify generation %d = %+v, %v; want accepted", generation, verdict, err)
		}
	}

	// Ending the previous secret early stops the old one and carries no
	// secret either.
	w = serveNSSKeys(r, http.MethodDelete, "/nss/keys/"+key.ID+"/previous", "")
	if w.Code != http.StatusOK {
		t.Fatalf("clear previous status = %d, body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"secret"`) || strings.Contains(w.Body.String(), `"previous_expires_at"`) {
		t.Errorf("clear previous carries a secret or a previous expiry: %s", w.Body.String())
	}
	if verdict, err := env.keys.VerifyFleetKey(ctx, key.ID, first); err != nil || verdict.OK {
		t.Errorf("verify the ended previous secret = %+v, %v; want refused", verdict, err)
	}
}

func TestNSSKeyController_ShouldAnswerConflicts(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	r := env.router(nssCallerNamed(t, nssCallerAdmin))
	fresh, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)

	w := serveNSSKeys(r, http.MethodDelete, "/nss/keys/"+fresh+"/previous", "")
	assertNSSError(t, w, http.StatusConflict, apitypes.ErrorCodeConflict)

	revoked, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
	nssRotateFirst(t, env, revoked)
	w = serveNSSKeys(r, http.MethodPost, "/nss/keys/"+revoked+"/revoke", "")
	if w.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, body = %s", w.Code, w.Body.String())
	}
	var key webtypes.NSSKeyResponse
	decodeEnvelope(t, w.Body.Bytes(), &key)
	if key.Status != model.NSSKeyStatusRevoked || key.RevokedAt == nil || key.RevokedByUsername != "nss-admin" {
		t.Errorf("revoked key = %s at %v by %q, want revoked by nss-admin", key.Status, key.RevokedAt, key.RevokedByUsername)
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "should refuse to edit when the key is revoked", method: http.MethodPatch, path: "/nss/keys/{id}", body: `{"description":"too late"}`},
		{name: "should refuse to reassign when the key is revoked", method: http.MethodPatch, path: "/nss/keys/{id}", body: `{"group_name":"team-ops"}`},
		{name: "should refuse to rotate when the key is revoked", method: http.MethodPost, path: "/nss/keys/{id}/rotate"},
		{name: "should refuse to end the previous secret when the key is revoked", method: http.MethodDelete, path: "/nss/keys/{id}/previous"},
		{name: "should refuse to revoke again when the key is revoked", method: http.MethodPost, path: "/nss/keys/{id}/revoke"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := serveNSSKeys(r, tc.method, strings.ReplaceAll(tc.path, "{id}", revoked), tc.body)
			assertNSSError(t, w, http.StatusConflict, apitypes.ErrorCodeConflict)
		})
	}
}

func TestNSSKeyController_ShouldRejectMalformedRequests(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	r := env.router(nssCallerNamed(t, nssCallerAdmin))
	id, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "should refuse a create when the body is cut short", method: http.MethodPost, path: "/nss/keys", body: `{"name":`},
		{name: "should refuse a create when the name is not a string", method: http.MethodPost, path: "/nss/keys", body: `{"name":5}`},
		{name: "should refuse a create when the name is missing", method: http.MethodPost, path: "/nss/keys", body: `{}`},
		{name: "should refuse a create when the expiry is past", method: http.MethodPost, path: "/nss/keys", body: `{"name":"x","expires_at":"` + past + `"}`},
		{name: "should refuse a create when the address is not one", method: http.MethodPost, path: "/nss/keys", body: `{"name":"x","notification_email":"not an address"}`},
		{name: "should refuse an edit when the body is cut short", method: http.MethodPatch, path: "/nss/keys/{id}", body: `{"description":`},
		{name: "should refuse an edit when there is nothing to change", method: http.MethodPatch, path: "/nss/keys/{id}", body: `{}`},
		{name: "should refuse an edit when the expiry is past", method: http.MethodPatch, path: "/nss/keys/{id}", body: `{"expires_at":"` + past + `"}`},
		{name: "should refuse a report when the format is xml", method: http.MethodGet, path: "/nss/keys/{id}/rotation-report?format=xml"},
		{name: "should refuse usage when the limit is not a number", method: http.MethodGet, path: "/nss/keys/{id}/usage?limit=abc"},
		{name: "should refuse usage when the cursor is unknown", method: http.MethodGet, path: "/nss/keys/{id}/usage?after=garbage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := serveNSSKeys(r, tc.method, strings.ReplaceAll(tc.path, "{id}", id), tc.body)
			assertNSSError(t, w, http.StatusBadRequest, apitypes.ErrorCodeInvalidRequest)
		})
	}
}

func TestNSSKeyController_ShouldAnswerNotFoundForAnUnknownKey(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	r := env.router(nssCallerNamed(t, nssCallerAdmin))

	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/nss/keys/nss_missing0", ""},
		{http.MethodPatch, "/nss/keys/nss_missing0", `{"description":"x"}`},
		{http.MethodPost, "/nss/keys/nss_missing0/rotate", ""},
		{http.MethodDelete, "/nss/keys/nss_missing0/previous", ""},
		{http.MethodPost, "/nss/keys/nss_missing0/revoke", ""},
		{http.MethodGet, "/nss/keys/nss_missing0/usage", ""},
		{http.MethodGet, "/nss/keys/nss_missing0/rotation-report", ""},
	} {
		t.Run("should answer not found when "+route.method+" "+route.path+" names no key", func(t *testing.T) {
			t.Parallel()

			assertNSSError(t, serveNSSKeys(r, route.method, route.path, route.body), http.StatusNotFound, apitypes.ErrorCodeNotFound)
		})
	}
}

// seedNSSKeyUsage writes one usage row for key from sourceIP today.
func seedNSSKeyUsage(t *testing.T, db *gorm.DB, keyID, sourceIP string, day time.Time, lookups int64) {
	t.Helper()
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	row := model.NSSKeyUsage{
		KeyID: keyID, SourceIP: sourceIP, Generation: 1, Day: day,
		Lookups: lookups, Misses: 1, FirstSeen: day.Add(time.Hour), LastSeen: day.Add(2 * time.Hour),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed usage: %v", err)
	}
}

func TestNSSKeyController_ShouldDownloadTheRotationReportAsCSV(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	id, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
	today := time.Now().UTC()
	seedNSSKeyUsage(t, env.db, id, "192.0.2.21", today, 7)
	seedNSSKeyUsage(t, env.db, id, "192.0.2.22", today, 3)
	r := env.router(nssCallerNamed(t, nssCallerAuditor))

	w := serveNSSKeys(r, http.MethodGet, "/nss/keys/"+id+"/rotation-report?format=csv", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", got)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="`+id+`-rotation-report.csv"`) {
		t.Errorf("Content-Disposition = %q, want an attachment named for the key", got)
	}
	records, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v, body: %s", err, w.Body.String())
	}
	header := []string{"source_ip", "bucket", "last_generation", "first_seen", "last_seen", "lookups"}
	if len(records) != 3 || !reflect.DeepEqual(records[0], header) {
		t.Fatalf("records = %q, want the header and one row per address", records)
	}
	byIP := map[string][]string{}
	for _, record := range records[1:] {
		byIP[record[0]] = record
	}
	if got := byIP["192.0.2.21"]; len(got) != 6 || got[1] != "switched" || got[2] != "1" || got[5] != "7" {
		t.Errorf("row for 192.0.2.21 = %q, want switched, generation 1, 7 lookups", got)
	}

	w = serveNSSKeys(r, http.MethodGet, "/nss/keys/"+id+"/rotation-report", "")
	var report webtypes.NSSRotationReportResponse
	decodeEnvelope(t, w.Body.Bytes(), &report)
	if report.KeyID != id || report.Switched != 2 || len(report.Rows) != 2 {
		t.Errorf("JSON report = %+v, want two switched rows for %s", report, id)
	}
}

func TestNSSKeyController_ShouldPageTheUsage(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	id, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
	today := time.Now().UTC()
	seedNSSKeyUsage(t, env.db, id, "192.0.2.31", today, 1)
	seedNSSKeyUsage(t, env.db, id, "192.0.2.32", today, 2)
	seedNSSKeyUsage(t, env.db, id, "192.0.2.31", today.AddDate(0, 0, -1), 3)
	r := env.router(nssCallerNamed(t, nssCallerMember))

	var first webtypes.NSSKeyUsageResponse
	decodeEnvelope(t, serveNSSKeys(r, http.MethodGet, "/nss/keys/"+id+"/usage?limit=2", "").Body.Bytes(), &first)
	if len(first.Rows) != 2 || first.Total != 3 || first.NextCursor == nil {
		t.Fatalf("first page = %+v, want two of three rows and a cursor", first)
	}
	if first.Rows[0].SourceIP != "192.0.2.31" || first.Rows[1].SourceIP != "192.0.2.32" {
		t.Errorf("first page addresses = %s, %s; want today's by address", first.Rows[0].SourceIP, first.Rows[1].SourceIP)
	}

	var second webtypes.NSSKeyUsageResponse
	decodeEnvelope(t, serveNSSKeys(r, http.MethodGet, "/nss/keys/"+id+"/usage?limit=2&after="+url.QueryEscape(*first.NextCursor), "").Body.Bytes(), &second)
	if len(second.Rows) != 1 || second.NextCursor != nil || second.Rows[0].Lookups != 3 {
		t.Errorf("second page = %+v, want yesterday's one row and no cursor", second)
	}
}

func TestNSSKeyController_ShouldListTheAllowedShells(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	w := serveNSSKeys(env.router(nssCallerNamed(t, nssCallerUnrelated)), http.MethodGet, "/nss/shells", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got webtypes.NSSShellsResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if !reflect.DeepEqual(got.AllowedShells, env.cfg.NSS.AllowedShells) {
		t.Errorf("allowed_shells = %v, want %v", got.AllowedShells, env.cfg.NSS.AllowedShells)
	}
}

func TestNSSKeyController_ShouldAnswerTheKeyLifetimesInSeconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		grace time.Duration
		want  webtypes.NSSKeySettingsResponse
	}{
		{name: "should answer both lifetimes when the grace is set", grace: 720 * time.Hour,
			want: webtypes.NSSKeySettingsResponse{DefaultLifetimeSeconds: 31536000, RotationGraceSeconds: 2592000}},
		{name: "should answer a zero grace when a rotation ends the previous secret at once", grace: 0,
			want: webtypes.NSSKeySettingsResponse{DefaultLifetimeSeconds: 31536000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newNSSKeyEnv(t)
			env.cfg.NSS.KeyRotationGrace = tt.grace
			w := serveNSSKeys(env.router(nssCallerNamed(t, nssCallerUnrelated)), http.MethodGet, "/nss/key-settings", "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			var got webtypes.NSSKeySettingsResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if got != tt.want {
				t.Errorf("key settings = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNSSKeyController_ShouldCarryEachKeysLastUseAndRecentAlerts(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	used, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
	unused, _ := createNSSKey(t, env.keys, nssCreatorIdentity(), nssKeyGroup)
	today := time.Now().UTC()
	seedNSSKeyUsage(t, env.db, used, "192.0.2.41", today, 4)
	missAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for _, alert := range []model.NSSAlert{
		{Kind: "miss_rate", Subject: used, LastRaisedAt: missAt},
		{Kind: "key_source", Subject: used, LastRaisedAt: missAt.Add(-8 * 24 * time.Hour)},
	} {
		if err := env.db.Create(&alert).Error; err != nil {
			t.Fatalf("seed alert: %v", err)
		}
	}
	lastSeen := time.Date(today.Year(), today.Month(), today.Day(), 2, 0, 0, 0, time.UTC)
	r := env.router(nssCallerNamed(t, nssCallerMember))

	var list webtypes.NSSKeyListResponse
	decodeEnvelope(t, serveNSSKeys(r, http.MethodGet, "/nss/keys", "").Body.Bytes(), &list)
	listed := map[string]webtypes.NSSKeyResponse{}
	for _, key := range list.Keys {
		listed[key.ID] = key
	}

	tests := []struct {
		name   string
		id     string
		absent []string
		check  func(webtypes.NSSKeyResponse) bool
	}{
		{name: "should carry the last use and the week's miss rate alert when the key was used", id: used,
			absent: []string{`"key_source_alert_at"`},
			check: func(key webtypes.NSSKeyResponse) bool {
				return key.LastUsedAt != nil && key.LastUsedAt.Equal(lastSeen) &&
					key.MissRateAlertAt != nil && key.MissRateAlertAt.Equal(missAt) && key.KeySourceAlertAt == nil
			}},
		{name: "should leave every activity field out when no host has used the key", id: unused,
			absent: []string{`"last_used_at"`, `"miss_rate_alert_at"`, `"key_source_alert_at"`},
			check: func(key webtypes.NSSKeyResponse) bool {
				return key.LastUsedAt == nil && key.MissRateAlertAt == nil && key.KeySourceAlertAt == nil
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := serveNSSKeys(r, http.MethodGet, "/nss/keys/"+tt.id, "")
			var got webtypes.NSSKeyResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if !tt.check(got) || !tt.check(listed[tt.id]) {
				t.Errorf("detail = %+v, listed = %+v; want the key's activity on both", got, listed[tt.id])
			}
			for _, field := range tt.absent {
				if strings.Contains(w.Body.String(), field) {
					t.Errorf("body = %s, want no %s", w.Body.String(), field)
				}
			}
		})
	}
}

func TestNSSKeyController_ShouldAnswerDisabledOnEveryRoute(t *testing.T) {
	t.Parallel()

	// A session check that refuses everyone: nss_disabled must come first.
	refuse := func(g *gin.Context) {
		g.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"data": nil, "error": "no session", "error_code": apitypes.ErrorCodeUnauthenticated})
	}
	r := newNSSKeyRouter(nil, nil, refuse)

	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/nss/shells", ""},
		{http.MethodGet, "/nss/key-settings", ""},
		{http.MethodGet, "/nss/keys", ""},
		{http.MethodPost, "/nss/keys", `{"name":"x"}`},
		{http.MethodGet, "/nss/keys/nss_any", ""},
		{http.MethodPatch, "/nss/keys/nss_any", `{"description":"x"}`},
		{http.MethodPost, "/nss/keys/nss_any/rotate", ""},
		{http.MethodDelete, "/nss/keys/nss_any/previous", ""},
		{http.MethodPost, "/nss/keys/nss_any/revoke", ""},
		{http.MethodGet, "/nss/keys/nss_any/usage", ""},
		{http.MethodGet, "/nss/keys/nss_any/rotation-report", ""},
		{http.MethodGet, "/nss/keys/nss_any/rotation-report?format=csv", ""},
	} {
		t.Run("should answer nss_disabled when "+route.method+" "+route.path+" is asked with NSS off", func(t *testing.T) {
			t.Parallel()

			assertNSSError(t, serveNSSKeys(r, route.method, route.path, route.body), http.StatusNotFound, apitypes.ErrorCodeNSSDisabled)
		})
	}
}

// TestNSSKeyController_ShouldKeepTheSecretOutOfTheAudit checks the
// mutations' audit rows: the key id is recorded, the secret never.
func TestNSSKeyController_ShouldKeepTheSecretOutOfTheAudit(t *testing.T) {
	t.Parallel()

	env := newNSSKeyEnv(t)
	r := env.router(nssCallerNamed(t, nssCallerAdmin))

	var created webtypes.NSSKeySecretResponse
	decodeEnvelope(t, serveNSSKeys(r, http.MethodPost, "/nss/keys", `{"name":"audited"}`).Body.Bytes(), &created)
	var rotated webtypes.NSSKeySecretResponse
	decodeEnvelope(t, serveNSSKeys(r, http.MethodPost, "/nss/keys/"+created.Key.ID+"/rotate", "").Body.Bytes(), &rotated)

	var events []model.AuditEvent
	actions := []string{string(service.AuditNSSKeyCreated), string(service.AuditNSSKeyRotated)}
	if err := env.db.Where("action IN ?", actions).Find(&events).Error; err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want created and rotated", len(events))
	}
	for _, event := range events {
		raw, _ := json.Marshal(event)
		if !strings.Contains(event.Payload, created.Key.ID) {
			t.Errorf("%s payload lacks the key id: %s", event.Action, event.Payload)
		}
		for _, secret := range []string{created.Secret, rotated.Secret} {
			if strings.Contains(string(raw), secret) {
				t.Errorf("%s carries a secret: %s", event.Action, raw)
			}
		}
	}
}
