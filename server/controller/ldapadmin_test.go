package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// fakeLDAPDiagnostics stands in for the directory service, recording what
// the handlers asked it for.
type fakeLDAPDiagnostics struct {
	probeResult *service.ProbeResult
	probeErr    error
	gotProbe    service.ProbeRequest

	run     *model.LDAPSyncRun
	syncErr error
	gotSync service.SyncOptions

	lastRun    *model.LDAPSyncRun
	lastRunErr error
	running    bool
}

func (f *fakeLDAPDiagnostics) Probe(_ context.Context, req service.ProbeRequest) (*service.ProbeResult, error) {
	f.gotProbe = req
	return f.probeResult, f.probeErr
}

func (f *fakeLDAPDiagnostics) SyncWithOptions(_ context.Context, opts service.SyncOptions) (*model.LDAPSyncRun, error) {
	f.gotSync = opts
	return f.run, f.syncErr
}

func (f *fakeLDAPDiagnostics) LastSyncRun(context.Context) (*model.LDAPSyncRun, error) {
	return f.lastRun, f.lastRunErr
}

func (f *fakeLDAPDiagnostics) SyncRunning() bool { return f.running }

// ldapTestConfig is a config with the three roles configured and a directory
// enabled, so the authorization tests exercise the real middleware.
func ldapAdminTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Admin.RequireGroup = "ssh-admins"
	cfg.Admin.SOCGroup = "soc"
	cfg.Admin.AuditorGroup = "auditors"
	cfg.LDAP = config.LDAPConfig{
		Enabled:    true,
		URL:        "ldaps://dir.example.net",
		BaseDN:     "dc=example,dc=net",
		UserFilter: "(uid={{.Username}})",
		Timeout:    5 * time.Second,
		Fields: map[string]config.LDAPField{
			"groups":         {Attribute: "memberOf"},
			"other_accounts": {Attribute: "altSecurityIdentities"},
		},
		Sync: config.LDAPSync{Interval: 15 * time.Minute, DisableAfter: 45 * time.Minute, Reenable: true},
	}
	return cfg
}

// ldapRouter wires the directory controller with the real authorization
// middleware and the given identity.
func ldapRouter(t *testing.T, cfg *config.Config, db *gorm.DB, identity *service.Identity, diagnostics service.LDAPDiagnostics) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	r.Use(identityMiddleware(identity))

	NewLDAPAdminController(
		&r.RouterGroup,
		cfg,
		db,
		diagnostics,
		identityMiddleware(identity),
		middleware.NewAdminAuthMiddleware(cfg).Add(),
		middleware.NewAuditorAuthMiddleware(cfg).Add(),
		func(c *gin.Context) { c.Next() },
		nil,
		service.NewAuditService(cfg, db),
	)
	return r
}

// doLDAPRequest issues one request and returns the recorder.
func doLDAPRequest(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		encoded, _ := json.Marshal(body) //nolint:errcheck // test fixtures are always encodable.
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestLDAPAdminRoutes_ShouldEnforceTheRoleSplit pins who may do what: the
// status read is auditor-scoped because it names no credential, and the sync
// and the probe are admin-only.
func TestLDAPAdminRoutes_ShouldEnforceTheRoleSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		groups     []string
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "an auditor may read the status",
			groups:     []string{"auditors"},
			method:     http.MethodGet,
			path:       "/admin/ldap/status",
			wantStatus: http.StatusOK,
		},
		{
			name:       "an admin may read the status",
			groups:     []string{"ssh-admins"},
			method:     http.MethodGet,
			path:       "/admin/ldap/status",
			wantStatus: http.StatusOK,
		},
		{
			name:       "an ordinary user may not read the status",
			groups:     []string{"ssh-users"},
			method:     http.MethodGet,
			path:       "/admin/ldap/status",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "an auditor may not run the sync",
			groups:     []string{"auditors"},
			method:     http.MethodPost,
			path:       "/admin/ldap/sync",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "a SOC member may not run the sync",
			groups:     []string{"soc"},
			method:     http.MethodPost,
			path:       "/admin/ldap/sync",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "an admin may run the sync",
			groups:     []string{"ssh-admins"},
			method:     http.MethodPost,
			path:       "/admin/ldap/sync",
			wantStatus: http.StatusOK,
		},
		{
			name:       "an auditor may not probe",
			groups:     []string{"auditors"},
			method:     http.MethodPost,
			path:       "/admin/ldap/probe",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "an admin may probe",
			groups:     []string{"ssh-admins"},
			method:     http.MethodPost,
			path:       "/admin/ldap/probe",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := newTestDB(t)
			fake := &fakeLDAPDiagnostics{
				run:         &model.LDAPSyncRun{ID: "run-1", StartedAt: time.Now()},
				probeResult: &service.ProbeResult{BaseDN: "dc=example,dc=net", FilterSent: "(uid=alice)"},
			}
			identity := &service.Identity{Subject: "sub-alice", Username: "alice", Groups: tt.groups}
			r := ldapRouter(t, ldapAdminTestConfig(), db, identity, fake)

			w := doLDAPRequest(r, tt.method, tt.path, nil)
			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d, body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestLDAPStatusHandler_ShouldDescribeTheProbeTargetWithoutCredentials keeps
// the auditor-readable status honest: it names the connection so an operator
// knows what is being probed, and never the password.
func TestLDAPStatusHandler_ShouldDescribeTheProbeTargetWithoutCredentials(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	cfg := ldapAdminTestConfig()
	cfg.LDAP.BindDN = "cn=svc,dc=example,dc=net"
	cfg.LDAP.BindPassword = "hunter2"

	finished := time.Now()
	fake := &fakeLDAPDiagnostics{
		running: true,
		lastRun: &model.LDAPSyncRun{
			ID: "run-1", StartedAt: time.Now().Add(-time.Minute), FinishedAt: &finished,
			Trigger: model.LDAPSyncTriggerManual, Instance: "ssoosshd-1",
			UsersSeen: 3, Found: 2, Missing: 1,
		},
	}
	identity := &service.Identity{Subject: "sub-alice", Username: "alice", Groups: []string{"auditors"}}
	r := ldapRouter(t, cfg, db, identity, fake)

	w := doLDAPRequest(r, http.MethodGet, "/admin/ldap/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("hunter2")) {
		t.Fatal("the status response carried the directory bind password")
	}

	var got webtypes.LDAPStatusResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if !got.Enabled {
		t.Error("enabled = false for a configured directory")
	}
	if got.BaseDN != "dc=example,dc=net" {
		t.Errorf("base_dn = %q, want the configured one", got.BaseDN)
	}
	if got.DisableAfterSeconds != 2700 {
		t.Errorf("disable_after_seconds = %d, want 2700", got.DisableAfterSeconds)
	}
	if !got.Running {
		t.Error("running = false while a pass is in progress")
	}
	if got.LastRun == nil || got.LastRun.ID != "run-1" {
		t.Fatalf("last_run = %+v, want the recorded pass", got.LastRun)
	}
	if got.LastRun.Missing != 1 {
		t.Errorf("last_run.missing = %d, want 1", got.LastRun.Missing)
	}
	if len(got.ConfiguredAttributes) != 2 {
		t.Errorf("configured_attributes = %v, want both configured attributes", got.ConfiguredAttributes)
	}
}

// TestLDAPStatusHandler_ShouldReportADisabledDirectory keeps a deployment
// without LDAP from looking like a broken one.
func TestLDAPStatusHandler_ShouldReportADisabledDirectory(t *testing.T) {
	t.Parallel()

	cfg := ldapAdminTestConfig()
	cfg.LDAP.Enabled = false
	identity := &service.Identity{Subject: "sub-alice", Groups: []string{"auditors"}}
	r := ldapRouter(t, cfg, newTestDB(t), identity, nil)

	w := doLDAPRequest(r, http.MethodGet, "/admin/ldap/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	var got webtypes.LDAPStatusResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Enabled {
		t.Error("enabled = true with no directory configured")
	}
}

// TestLDAPSyncHandler_ShouldPassTheDryRunThrough covers the option that makes
// the button safe to press during an incident.
func TestLDAPSyncHandler_ShouldPassTheDryRunThrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       webtypes.LDAPSyncRequestBody
		wantDryRun bool
	}{
		{name: "a plain sync is not a dry run", body: webtypes.LDAPSyncRequestBody{}},
		{name: "a dry run is passed through", body: webtypes.LDAPSyncRequestBody{DryRun: true}, wantDryRun: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := newTestDB(t)
			actor := model.User{ID: "u-1", Subject: "sub-alice", Username: "alice", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := db.Create(&actor).Error; err != nil {
				t.Fatalf("seed actor: %v", err)
			}

			fake := &fakeLDAPDiagnostics{run: &model.LDAPSyncRun{
				ID: "run-1", StartedAt: time.Now(), Trigger: model.LDAPSyncTriggerManual,
				DryRun: tt.wantDryRun, ActorUserID: &actor.ID, Disabled: 2,
			}}
			identity := &service.Identity{Subject: "sub-alice", Username: "alice", Groups: []string{"ssh-admins"}}
			r := ldapRouter(t, ldapAdminTestConfig(), db, identity, fake)

			w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/sync", tt.body)
			if w.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
			}
			if fake.gotSync.DryRun != tt.wantDryRun {
				t.Errorf("dry run reached the service as %v, want %v", fake.gotSync.DryRun, tt.wantDryRun)
			}
			if fake.gotSync.Trigger != model.LDAPSyncTriggerManual {
				t.Errorf("trigger = %q, want manual", fake.gotSync.Trigger)
			}
			if fake.gotSync.ActorUserID != actor.ID {
				t.Errorf("actor = %q, want the calling admin's users-row id", fake.gotSync.ActorUserID)
			}

			var got webtypes.LDAPSyncRunResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if got.DryRun != tt.wantDryRun {
				t.Errorf("response dry_run = %v, want %v", got.DryRun, tt.wantDryRun)
			}
			if got.ActorUsername != "alice" {
				t.Errorf("actor_username = %q, want alice", got.ActorUsername)
			}
		})
	}
}

// TestLDAPSyncHandler_ShouldRefuseASecondPass is the guard that stops an
// impatient operator stacking passes over the same users.
func TestLDAPSyncHandler_ShouldRefuseASecondPass(t *testing.T) {
	t.Parallel()

	fake := &fakeLDAPDiagnostics{syncErr: service.ErrSyncInProgress}
	identity := &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}}
	r := ldapRouter(t, ldapAdminTestConfig(), newTestDB(t), identity, fake)

	w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/sync", webtypes.LDAPSyncRequestBody{})
	if w.Code != http.StatusConflict {
		t.Errorf("got status %d, want 409: %s", w.Code, w.Body.String())
	}
}

// TestLDAPSyncHandler_ShouldReportAFailedPassAsItsRecord keeps an unreachable
// directory answerable: the run row says the pass ran and why it failed,
// which is what the operator pressed the button to find out.
func TestLDAPSyncHandler_ShouldReportAFailedPassAsItsRecord(t *testing.T) {
	t.Parallel()

	finished := time.Now()
	fake := &fakeLDAPDiagnostics{
		run: &model.LDAPSyncRun{
			ID: "run-1", StartedAt: time.Now(), FinishedAt: &finished,
			ErrorMessage: "directory sync could not connect: connection refused",
		},
		syncErr: errors.New("directory sync could not connect: connection refused"),
	}
	identity := &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}}
	r := ldapRouter(t, ldapAdminTestConfig(), newTestDB(t), identity, fake)

	w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/sync", webtypes.LDAPSyncRequestBody{})
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 carrying the failed run: %s", w.Code, w.Body.String())
	}

	var got webtypes.LDAPSyncRunResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Error == "" {
		t.Error("the response carried no error for a pass that could not connect")
	}
}

// TestLDAPSyncHandler_ShouldAuditTheTrigger records who ran it and what it
// concluded, on the same terms as every other privileged action.
func TestLDAPSyncHandler_ShouldAuditTheTrigger(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	fake := &fakeLDAPDiagnostics{run: &model.LDAPSyncRun{
		ID: "run-1", StartedAt: time.Now(), DryRun: true, Disabled: 2, UsersSeen: 9,
	}}
	identity := &service.Identity{Subject: "sub-alice", Username: "alice", Groups: []string{"ssh-admins"}}
	r := ldapRouter(t, ldapAdminTestConfig(), db, identity, fake)

	if w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/sync", webtypes.LDAPSyncRequestBody{DryRun: true}); w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	events := auditEventsMentioning(t, db, string(service.AuditLDAPSyncTriggered))
	if len(events) != 1 {
		t.Fatalf("recorded %d sync events, want 1", len(events))
	}
	if !bytes.Contains([]byte(events[0].Payload), []byte(`"dry_run":true`)) {
		t.Errorf("the sync event did not record that it was a dry run: %s", events[0].Payload)
	}
}

// TestLDAPProbeHandler_ShouldResolveBindings covers the three binding
// sources. Typed values are what let an admin test an entry before that
// person has ever logged in.
func TestLDAPProbeHandler_ShouldResolveBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		body         webtypes.LDAPProbeRequestBody
		seedUser     bool
		wantStatus   int
		wantUsername string
	}{
		{
			name:         "self binds against the calling admin",
			body:         webtypes.LDAPProbeRequestBody{BindingSource: "self"},
			wantStatus:   http.StatusOK,
			wantUsername: "alice",
		},
		{
			name:         "an absent binding source defaults to self",
			body:         webtypes.LDAPProbeRequestBody{},
			wantStatus:   http.StatusOK,
			wantUsername: "alice",
		},
		{
			name:         "custom binds against typed values",
			body:         webtypes.LDAPProbeRequestBody{BindingSource: "custom", Bindings: &webtypes.LDAPProbeBindings{Username: "never-logged-in"}},
			wantStatus:   http.StatusOK,
			wantUsername: "never-logged-in",
		},
		{
			name:         "user binds against an existing user",
			body:         webtypes.LDAPProbeRequestBody{BindingSource: "user", UserID: "u-bob"},
			seedUser:     true,
			wantStatus:   http.StatusOK,
			wantUsername: "bob",
		},
		{
			name:       "user with no id is refused",
			body:       webtypes.LDAPProbeRequestBody{BindingSource: "user"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "user that does not exist is a 404",
			body:       webtypes.LDAPProbeRequestBody{BindingSource: "user", UserID: "u-nobody"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "custom with no bindings is refused",
			body:       webtypes.LDAPProbeRequestBody{BindingSource: "custom"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "an unknown binding source is refused",
			body:       webtypes.LDAPProbeRequestBody{BindingSource: "everyone"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := newTestDB(t)
			if tt.seedUser {
				bob := model.User{
					ID: "u-bob", Subject: "sub-bob", Username: "bob", Email: "bob@example.com",
					ExtraFields: `{"employee_id":"E-2","teams":["a","b"]}`,
					CreatedAt:   time.Now(), UpdatedAt: time.Now(),
				}
				if err := db.Create(&bob).Error; err != nil {
					t.Fatalf("seed user: %v", err)
				}
			}

			fake := &fakeLDAPDiagnostics{probeResult: &service.ProbeResult{
				BaseDN: "dc=example,dc=net", FilterSent: "(uid=x)", Mode: service.ProbeFilterTemplate,
			}}
			identity := &service.Identity{Subject: "sub-alice", Username: "alice", Groups: []string{"ssh-admins"}}
			r := ldapRouter(t, ldapAdminTestConfig(), db, identity, fake)

			w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/probe", tt.body)
			if w.Code != tt.wantStatus {
				t.Fatalf("got status %d, want %d: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantUsername != "" && fake.gotProbe.Bindings.Username != tt.wantUsername {
				t.Errorf("bound username = %q, want %q", fake.gotProbe.Bindings.Username, tt.wantUsername)
			}
		})
	}
}

// TestLDAPProbeHandler_ShouldBindStoredScalarExtras keeps a list-valued extra
// out of a filter: a filter interpolates one value, and joining a list would
// produce a filter nobody wrote.
func TestLDAPProbeHandler_ShouldBindStoredScalarExtras(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	bob := model.User{
		ID: "u-bob", Subject: "sub-bob", Username: "bob",
		ExtraFields: `{"employee_id":"E-2","teams":["a","b"]}`,
		CreatedAt:   time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(&bob).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	fake := &fakeLDAPDiagnostics{probeResult: &service.ProbeResult{BaseDN: "dc=example,dc=net", FilterSent: "(uid=bob)"}}
	identity := &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}}
	r := ldapRouter(t, ldapAdminTestConfig(), db, identity, fake)

	w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/probe", webtypes.LDAPProbeRequestBody{
		BindingSource: "user", UserID: "u-bob",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	if fake.gotProbe.Bindings.Extra["employee_id"] != "E-2" {
		t.Errorf("scalar extra = %q, want E-2", fake.gotProbe.Bindings.Extra["employee_id"])
	}
	if _, ok := fake.gotProbe.Bindings.Extra["teams"]; ok {
		t.Error("a list-valued extra reached the filter bindings")
	}
}

// TestLDAPProbeHandler_ShouldValidateTheMode keeps the two modes explicit:
// escaping happens in one of them and not the other, so a typo must not
// silently pick one.
func TestLDAPProbeHandler_ShouldValidateTheMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       string
		wantStatus int
		wantMode   service.ProbeFilterMode
	}{
		{name: "empty defaults to template", mode: "", wantStatus: http.StatusOK, wantMode: service.ProbeFilterTemplate},
		{name: "template is accepted", mode: "template", wantStatus: http.StatusOK, wantMode: service.ProbeFilterTemplate},
		{name: "literal is accepted", mode: "literal", wantStatus: http.StatusOK, wantMode: service.ProbeFilterLiteral},
		{name: "anything else is refused", mode: "raw", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeLDAPDiagnostics{probeResult: &service.ProbeResult{BaseDN: "dc=example,dc=net", FilterSent: "(uid=alice)"}}
			identity := &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}}
			r := ldapRouter(t, ldapAdminTestConfig(), newTestDB(t), identity, fake)

			w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/probe", webtypes.LDAPProbeRequestBody{Mode: tt.mode})
			if w.Code != tt.wantStatus {
				t.Fatalf("got status %d, want %d: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantMode != "" && fake.gotProbe.Mode != tt.wantMode {
				t.Errorf("mode reached the service as %q, want %q", fake.gotProbe.Mode, tt.wantMode)
			}
		})
	}
}

// TestLDAPProbeHandler_ShouldCarryTheNoWriteGuarantee keeps the promise in
// the response rather than only in the documentation.
func TestLDAPProbeHandler_ShouldCarryTheNoWriteGuarantee(t *testing.T) {
	t.Parallel()

	fake := &fakeLDAPDiagnostics{probeResult: &service.ProbeResult{
		BaseDN: "dc=example,dc=net", FilterSent: "(uid=alice)", Mode: service.ProbeFilterTemplate,
		Matched: 1,
		Entry: &service.ProbeEntry{DN: "uid=alice,dc=example,dc=net", Attributes: []service.ProbeAttribute{
			{Name: "uid", Values: []string{"alice"}, Configured: true},
		}},
		Merge:       []service.ProbeMerge{{Name: "groups", Action: "persist-groups", Dropped: []string{"vpn-legacy"}}},
		Suggestions: []service.ProbeSuggestion{{Reason: "dropped group", YAML: "ldap:\n  sync:\n    extra_groups: [\"vpn-legacy\"]"}},
	}}
	identity := &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}}
	r := ldapRouter(t, ldapAdminTestConfig(), newTestDB(t), identity, fake)

	w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/probe", webtypes.LDAPProbeRequestBody{})
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	var got webtypes.LDAPProbeResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if got.Wrote {
		t.Error("the probe response claimed it wrote something")
	}
	if got.Entry == nil || len(got.Entry.Attributes) != 1 {
		t.Fatalf("entry = %+v, want the returned attribute", got.Entry)
	}
	if len(got.Suggestions) != 1 {
		t.Errorf("suggestions = %v, want the dropped group's config line", got.Suggestions)
	}
	if len(got.Merge) != 1 || got.Merge[0].Dropped[0] != "vpn-legacy" {
		t.Errorf("merge = %+v, want the dropped group named", got.Merge)
	}
}

// TestLDAPProbeHandler_ShouldReportAProbeThatCouldNotRun keeps a bad filter
// or an unreachable directory readable as a question that could not be
// asked, rather than a server fault.
func TestLDAPProbeHandler_ShouldReportAProbeThatCouldNotRun(t *testing.T) {
	t.Parallel()

	fake := &fakeLDAPDiagnostics{probeErr: errors.New("could not reach the directory: connection refused")}
	identity := &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}}
	r := ldapRouter(t, ldapAdminTestConfig(), newTestDB(t), identity, fake)

	w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/probe", webtypes.LDAPProbeRequestBody{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestLDAPProbeHandler_ShouldAuditEveryProbe records the filter that went
// out. The probe writes nothing, but it makes the server open an outbound
// connection and read an entry in full.
func TestLDAPProbeHandler_ShouldAuditEveryProbe(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	fake := &fakeLDAPDiagnostics{probeResult: &service.ProbeResult{
		BaseDN: "dc=example,dc=net", FilterSent: "(uid=alice)", Mode: service.ProbeFilterTemplate, Matched: 1,
	}}
	identity := &service.Identity{Subject: "sub-alice", Username: "alice", Groups: []string{"ssh-admins"}}
	r := ldapRouter(t, ldapAdminTestConfig(), db, identity, fake)

	if w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/probe", webtypes.LDAPProbeRequestBody{}); w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}

	events := auditEventsMentioning(t, db, string(service.AuditLDAPProbed))
	if len(events) != 1 {
		t.Fatalf("recorded %d probe events, want 1", len(events))
	}
	if !bytes.Contains([]byte(events[0].Payload), []byte("(uid=alice)")) {
		t.Errorf("the probe event did not record the filter it sent: %s", events[0].Payload)
	}
}

// TestLDAPAdminHandlers_ShouldRefuseWithoutADirectory keeps the write
// endpoints from pretending to work when nothing is configured.
func TestLDAPAdminHandlers_ShouldRefuseWithoutADirectory(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/admin/ldap/sync", "/admin/ldap/probe"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			cfg := ldapAdminTestConfig()
			cfg.LDAP.Enabled = false
			identity := &service.Identity{Subject: "sub-alice", Groups: []string{"ssh-admins"}}
			r := ldapRouter(t, cfg, newTestDB(t), identity, nil)

			w := doLDAPRequest(r, http.MethodPost, path, nil)
			if w.Code != http.StatusBadRequest {
				t.Errorf("got status %d, want 400: %s", w.Code, w.Body.String())
			}
		})
	}
}

// auditEventsMentioning returns the recorded events whose payload names the
// given action. The action lives inside the JSON payload rather than in a
// column of its own, so this is a substring match on it.
func auditEventsMentioning(t *testing.T, db *gorm.DB, action string) []model.AuditEvent {
	t.Helper()

	var all []model.AuditEvent
	if err := db.Find(&all).Error; err != nil {
		t.Fatalf("load audit events: %v", err)
	}
	var out []model.AuditEvent
	for _, event := range all {
		if bytes.Contains([]byte(event.Payload), []byte(`"action":"`+action+`"`)) {
			out = append(out, event)
		}
	}
	return out
}
