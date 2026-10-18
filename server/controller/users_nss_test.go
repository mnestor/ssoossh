package controller

// Test methodology: /users/me and /users/me/shell through the user
// controller behind the error middleware, with identityMiddleware in place
// of the session and CSRF passing through. The /users/me contract is
// driven by a fake service.PosixAccountProvider; one case, and the shell
// route, use the real NSSLookupService on testdb.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/test/testdb"
)

// fakePosixAccounts is a service.PosixAccountProvider answering with a
// fixed account or error, and counting the reads.
type fakePosixAccounts struct {
	account *service.PosixAccount
	err     error
	reads   int
}

// AccountForIdentity returns the fixed account.
func (f *fakePosixAccounts) AccountForIdentity(_ context.Context, _ *service.Identity) (*service.PosixAccount, error) {
	f.reads++
	return f.account, f.err
}

// SetShellPref stores pref on the fixed account.
func (f *fakePosixAccounts) SetShellPref(_ context.Context, _ *service.Identity, pref *string, _ string) (*service.PosixAccount, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.account.ShellPref = pref
	return f.account, nil
}

// newNSSUserRouter mounts the user controller for identity with posix as
// its POSIX account source.
func newNSSUserRouter(cfg *config.Config, identity *service.Identity, posix service.PosixAccountProvider) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewUserController(&r.RouterGroup, cfg, identityMiddleware(identity), &mockUserDatabase{}, nil, func(g *gin.Context) { g.Next() }, posix)
	return r
}

// currentUserFields reads GET /users/me's data as raw fields, so a test
// can tell null from absent.
func currentUserFields(t *testing.T, r *gin.Engine) map[string]json.RawMessage {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var fields map[string]json.RawMessage
	decodeEnvelope(t, w.Body.Bytes(), &fields)
	return fields
}

func TestCurrentUserHandler_ShouldReportTheNSSKeyAdminRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		keyGroup string
		groups   []string
		want     bool
	}{
		{name: "should report the role when the caller is in admin.nss_key_group", keyGroup: "nss-key-admins", groups: []string{"nss-key-admins"}, want: true},
		{name: "should report the role when the caller is an admin", keyGroup: "nss-key-admins", groups: []string{"ssh-admins"}, want: true},
		{name: "should report the role when the caller is an admin and no key group is set", keyGroup: "", groups: []string{"ssh-admins"}, want: true},
		{name: "should not report the role when the caller is SOC", keyGroup: "nss-key-admins", groups: []string{"ssh-soc"}},
		{name: "should not report the role when the caller is an auditor", keyGroup: "nss-key-admins", groups: []string{"ssh-auditors"}},
		{name: "should not report the role when the caller is a plain user", keyGroup: "nss-key-admins", groups: []string{"ssh-users"}},
		{name: "should not report the role when the caller is in no group", keyGroup: "nss-key-admins", groups: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := newNSSTestConfig(t)
			cfg.Admin.NSSKeyGroup = tc.keyGroup
			r := newNSSUserRouter(cfg, &service.Identity{Subject: "sub-1", Groups: tc.groups}, nil)

			if got := string(currentUserFields(t, r)["is_nss_key_admin"]); got != boolJSON(tc.want) {
				t.Errorf("is_nss_key_admin = %s, want %s", got, boolJSON(tc.want))
			}
		})
	}
}

// boolJSON is b as JSON.
func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestCurrentUserHandler_ShouldReportWhetherNSSIsEnabled(t *testing.T) {
	t.Parallel()

	for _, enabled := range []bool{true, false} {
		t.Run("should report nss_enabled "+boolJSON(enabled)+" when the config says so", func(t *testing.T) {
			t.Parallel()

			cfg := newNSSTestConfig(t)
			cfg.NSS.Enabled = enabled
			r := newNSSUserRouter(cfg, &service.Identity{Subject: "sub-1"}, nil)

			if got := string(currentUserFields(t, r)["nss_enabled"]); got != boolJSON(enabled) {
				t.Errorf("nss_enabled = %s, want %s", got, boolJSON(enabled))
			}
		})
	}
}

func TestCurrentUserHandler_ShouldReportTheNSSPosixAccount(t *testing.T) {
	t.Parallel()

	linked := &service.PosixAccount{UID: 1500, Name: "alice", ShellPref: ptrTo("zsh")}
	tests := []struct {
		name      string
		enabled   bool
		posix     *fakePosixAccounts
		nilPosix  bool
		want      string
		wantReads int
	}{
		{name: "should report the account when one is linked", enabled: true, posix: &fakePosixAccounts{account: linked},
			want: `{"uid":1500,"name":"alice","shell_pref":"zsh"}`, wantReads: 1},
		{name: "should report a null shell_pref when the person chose none", enabled: true, posix: &fakePosixAccounts{account: &service.PosixAccount{UID: 1501, Name: "bob"}},
			want: `{"uid":1501,"name":"bob","shell_pref":null}`, wantReads: 1},
		{name: "should report null when no account is linked", enabled: true, posix: &fakePosixAccounts{}, want: "null", wantReads: 1},
		{name: "should report null without reading when NSS is off", enabled: false, posix: &fakePosixAccounts{account: linked}, want: "null", wantReads: 0},
		{name: "should report null when there is no account source", enabled: true, nilPosix: true, want: "null"},
		{name: "should report null rather than fail when the read fails", enabled: true, posix: &fakePosixAccounts{err: errors.New("database is down")}, want: "null", wantReads: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := newNSSTestConfig(t)
			cfg.NSS.Enabled = tc.enabled
			var posix service.PosixAccountProvider
			if !tc.nilPosix {
				posix = tc.posix
			}
			r := newNSSUserRouter(cfg, &service.Identity{Subject: "sub-1"}, posix)

			fields := currentUserFields(t, r)
			got, present := fields["posix"]
			if !present {
				t.Fatalf("posix is absent from %v, want it present", fields)
			}
			if string(got) != tc.want {
				t.Errorf("posix = %s, want %s", got, tc.want)
			}
			if !tc.nilPosix && tc.posix.reads != tc.wantReads {
				t.Errorf("account reads = %d, want %d", tc.posix.reads, tc.wantReads)
			}
		})
	}
}

// nssShellEnv is a database with a person linked to a POSIX account, a
// person with none, and the real lookup service, audited.
type nssShellEnv struct {
	db      *gorm.DB
	cfg     *config.Config
	lookups *service.NSSLookupService
}

// The people the shell tests act as.
const (
	nssLinkedPerson   = "nss-linked"
	nssUnlinkedPerson = "nss-unlinked"
)

// newNSSShellEnv seeds alice (1500), linked to nssLinkedPerson, with
// shellPref as her stored choice.
func newNSSShellEnv(t *testing.T, shellPref *string) *nssShellEnv {
	t.Helper()
	db := testdb.New(t)
	cfg := newNSSTestConfig(t)
	seedPlainUser(t, db, nssLinkedPerson)
	seedPlainUser(t, db, nssUnlinkedPerson)
	linked := nssLinkedPerson
	seedNSSPosixUser(t, db, model.PosixUser{Name: "alice", UID: 1500, GID: 100, UserID: &linked, ShellPref: shellPref})
	lookups := service.NewNSSLookupService(cfg, db, nil)
	lookups.SetAuditor(service.NewAuditService(cfg, db))
	return &nssShellEnv{db: db, cfg: cfg, lookups: lookups}
}

// storedShellPref reads alice's shell_pref back from the database.
func (e *nssShellEnv) storedShellPref(t *testing.T) *string {
	t.Helper()
	var row model.PosixUser
	if err := e.db.Where("uid = ?", 1500).Take(&row).Error; err != nil {
		t.Fatalf("read posix account: %v", err)
	}
	return row.ShellPref
}

// shellAudits counts the nss.shell_changed audit rows.
func (e *nssShellEnv) shellAudits(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := e.db.Model(&model.AuditEvent{}).Where("action = ?", string(service.AuditNSSShellChanged)).Count(&n).Error; err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func TestCurrentUserHandler_ShouldReadTheLinkedNSSAccountFromTheDatabase(t *testing.T) {
	t.Parallel()

	env := newNSSShellEnv(t, ptrTo("fish"))
	tests := []struct {
		name    string
		subject string
		want    string
	}{
		{name: "should report the account when the person's row is linked", subject: "sub-" + nssLinkedPerson, want: `{"uid":1500,"name":"alice","shell_pref":"fish"}`},
		{name: "should report null when the person's row is not linked", subject: "sub-" + nssUnlinkedPerson, want: "null"},
		{name: "should report null when the session has no users row", subject: "sub-nobody", want: "null"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newNSSUserRouter(env.cfg, &service.Identity{Subject: tc.subject}, env.lookups)
			if got := string(currentUserFields(t, r)["posix"]); got != tc.want {
				t.Errorf("posix = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestUpdateShellHandler_NSS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		stored      *string
		subject     string
		body        string
		wantStatus  int
		wantCode    string
		wantPrefix  string
		wantPosix   string
		wantStored  *string
		wantAudited int64
	}{
		{name: "should store an allowed shell when the person chooses one", subject: "sub-" + nssLinkedPerson, body: `{"shell_pref":"zsh"}`,
			wantStatus: http.StatusOK, wantPosix: `{"uid":1500,"name":"alice","shell_pref":"zsh"}`, wantStored: ptrTo("zsh"), wantAudited: 1},
		{name: "should replace the stored shell when the person chooses another", stored: ptrTo("bash"), subject: "sub-" + nssLinkedPerson, body: `{"shell_pref":"fish"}`,
			wantStatus: http.StatusOK, wantPosix: `{"uid":1500,"name":"alice","shell_pref":"fish"}`, wantStored: ptrTo("fish"), wantAudited: 1},
		{name: "should clear the shell when the person sends null", stored: ptrTo("zsh"), subject: "sub-" + nssLinkedPerson, body: `{"shell_pref":null}`,
			wantStatus: http.StatusOK, wantPosix: `{"uid":1500,"name":"alice","shell_pref":null}`, wantAudited: 1},
		{name: "should clear the shell when the field is left out", stored: ptrTo("zsh"), subject: "sub-" + nssLinkedPerson, body: `{}`,
			wantStatus: http.StatusOK, wantPosix: `{"uid":1500,"name":"alice","shell_pref":null}`, wantAudited: 1},
		{name: "should write nothing when the shell is already stored", stored: ptrTo("zsh"), subject: "sub-" + nssLinkedPerson, body: `{"shell_pref":"zsh"}`,
			wantStatus: http.StatusOK, wantPosix: `{"uid":1500,"name":"alice","shell_pref":"zsh"}`, wantStored: ptrTo("zsh"), wantAudited: 0},
		{name: "should refuse a shell when it is not in the allowed list", stored: ptrTo("bash"), subject: "sub-" + nssLinkedPerson, body: `{"shell_pref":"tcsh"}`,
			wantStatus: http.StatusBadRequest, wantCode: apitypes.ErrorCodeInvalidRequest, wantPrefix: "shell_not_allowed", wantStored: ptrTo("bash")},
		{name: "should refuse a shell when it is sent as a path", subject: "sub-" + nssLinkedPerson, body: `{"shell_pref":"/bin/zsh"}`,
			wantStatus: http.StatusBadRequest, wantCode: apitypes.ErrorCodeInvalidRequest, wantPrefix: "shell_not_allowed"},
		{name: "should refuse an empty shell name when one is sent", subject: "sub-" + nssLinkedPerson, body: `{"shell_pref":""}`,
			wantStatus: http.StatusBadRequest, wantCode: apitypes.ErrorCodeInvalidRequest, wantPrefix: "shell_not_allowed"},
		{name: "should refuse a malformed body", subject: "sub-" + nssLinkedPerson, body: `{"shell_pref":`,
			wantStatus: http.StatusBadRequest, wantCode: apitypes.ErrorCodeInvalidRequest},
		{name: "should answer not found when no account is linked to the person", subject: "sub-" + nssUnlinkedPerson, body: `{"shell_pref":"zsh"}`,
			wantStatus: http.StatusNotFound, wantCode: apitypes.ErrorCodeNotFound},
		{name: "should refuse a session when it has no users row", subject: "sub-nobody", body: `{"shell_pref":"zsh"}`,
			wantStatus: http.StatusForbidden, wantCode: apitypes.ErrorCodeForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newNSSShellEnv(t, tc.stored)
			r := newNSSUserRouter(env.cfg, &service.Identity{Subject: tc.subject}, env.lookups)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/users/me/shell", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				var got json.RawMessage
				decodeEnvelope(t, w.Body.Bytes(), &got)
				if string(got) != tc.wantPosix {
					t.Errorf("account = %s, want %s", got, tc.wantPosix)
				}
			} else {
				code, message := nssEnvelopeError(t, w.Body.Bytes())
				if code != tc.wantCode {
					t.Errorf("error_code = %q, want %q", code, tc.wantCode)
				}
				if !strings.HasPrefix(message, tc.wantPrefix) {
					t.Errorf("error = %q, want it to start with %q", message, tc.wantPrefix)
				}
			}
			stored := env.storedShellPref(t)
			if (stored == nil) != (tc.wantStored == nil) || (stored != nil && *stored != *tc.wantStored) {
				t.Errorf("stored shell_pref = %v, want %v", optionalShell(stored), optionalShell(tc.wantStored))
			}
			if got := env.shellAudits(t); got != tc.wantAudited {
				t.Errorf("nss.shell_changed rows = %d, want %d", got, tc.wantAudited)
			}
		})
	}
}

// optionalShell renders an optional shell for a failure message.
func optionalShell(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestUpdateShellHandler_ShouldAnswerNSSDisabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		enabled  bool
		nilPosix bool
	}{
		{name: "should answer nss_disabled when nss.enabled is false", enabled: false},
		{name: "should answer nss_disabled when there is no account source", enabled: true, nilPosix: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := newNSSTestConfig(t)
			cfg.NSS.Enabled = tc.enabled
			fake := &fakePosixAccounts{account: &service.PosixAccount{UID: 1500, Name: "alice"}}
			var posix service.PosixAccountProvider = fake
			if tc.nilPosix {
				posix = nil
			}
			r := newNSSUserRouter(cfg, &service.Identity{Subject: "sub-1"}, posix)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/users/me/shell", bytes.NewBufferString(`{"shell_pref":"zsh"}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			assertNSSError(t, w, http.StatusNotFound, apitypes.ErrorCodeNSSDisabled)
			if fake.account.ShellPref != nil {
				t.Errorf("shell_pref = %q, want nothing stored", *fake.account.ShellPref)
			}
		})
	}
}
