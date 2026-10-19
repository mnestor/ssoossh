package service

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/test/testdb"
)

// The callers of the fleet key role matrix, one per role, each with a
// users row seeded by seedNSSCallers.
var (
	nssAdmin    = &Identity{Subject: "sub-admin", Username: "root-admin", Email: "admin@example.org", Groups: []string{"admins"}}
	nssKeyAdmin = &Identity{Subject: "sub-keyadmin", Username: "kim", Email: "kim@example.org", Groups: []string{"nss-admins"}}
	nssSOC      = &Identity{Subject: "sub-soc", Username: "sam", Groups: []string{"soc"}}
	nssAuditor  = &Identity{Subject: "sub-auditor", Username: "ava", Groups: []string{"auditors"}}
	nssMember   = &Identity{Subject: "sub-member", Username: "mo", Groups: []string{"web-admins"}}
	nssPlain    = &Identity{Subject: "sub-plain", Username: "pat", Groups: []string{"engineering"}}
)

// seedNSSCallers writes the users rows of the six callers and returns
// their ids by subject.
func seedNSSCallers(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()

	ids := map[string]string{}
	for _, identity := range []*Identity{nssAdmin, nssKeyAdmin, nssSOC, nssAuditor, nssMember, nssPlain} {
		ids[identity.Subject] = seedNSSPerson(t, db, identity.Subject, identity.Username, nil)
	}
	return ids
}

// newTestNSSKeys builds the key service over db at the fixed clock, with
// an auditor and a capturing notifier.
func newTestNSSKeys(t *testing.T, c *config.Config, db *gorm.DB) (*NSSKeyService, *capturingNotifier) {
	t.Helper()

	keys, err := NewNSSKeyService(c, db, nil)
	if err != nil {
		t.Fatalf("NewNSSKeyService: %v", err)
	}
	keys.now = nssLookupClock()
	keys.SetAuditor(NewAuditService(c, db))
	notifier := &capturingNotifier{}
	keys.SetNotifier(notifier)
	return keys, notifier
}

// createNSSKey creates a key managed by group as the key admin and returns
// it with its secret.
func createNSSKey(t *testing.T, keys *NSSKeyService, name, group string) (*NSSKeyView, string) {
	t.Helper()

	view, secret, err := keys.Create(t.Context(), nssKeyAdmin, NSSKeyCreate{Name: name, GroupName: group}, "192.0.2.1")
	if err != nil {
		t.Fatalf("create key %q: %v", name, err)
	}
	return view, secret
}

// auditDetails returns the detail of every audit row recorded for action.
func auditDetails(t *testing.T, db *gorm.DB, action AuditAction) []map[string]any {
	t.Helper()

	var rows []model.AuditEvent
	if err := db.Where("action = ?", string(action)).Order("created_at").Find(&rows).Error; err != nil {
		t.Fatalf("read audit: %v", err)
	}
	details := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var payload struct {
			Detail map[string]any `json:"detail"`
		}
		if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
			t.Fatalf("decode audit: %v", err)
		}
		details = append(details, payload.Detail)
	}
	return details
}

func TestNSSKeys_ShouldGrantEachRoleItsPermissionsWhenAKeyIsViewed(t *testing.T) {
	t.Parallel()

	all := []model.NSSKeyPermission{model.NSSKeyPermissionView, model.NSSKeyPermissionRotate, model.NSSKeyPermissionRevoke, model.NSSKeyPermissionEdit, model.NSSKeyPermissionAssignGroup}
	tests := []struct {
		name        string
		caller      *Identity
		revoked     bool
		want        []model.NSSKeyPermission
		wantVisible bool
	}{
		{name: "should grant everything when the caller is an admin", caller: nssAdmin, want: all, wantVisible: true},
		{name: "should grant everything when the caller holds the key admin role", caller: nssKeyAdmin, want: all, wantVisible: true},
		{name: "should grant view and revoke when the caller is SOC", caller: nssSOC, want: []model.NSSKeyPermission{model.NSSKeyPermissionView, model.NSSKeyPermissionRevoke}, wantVisible: true},
		{name: "should grant view alone when the caller is an auditor", caller: nssAuditor, want: []model.NSSKeyPermission{model.NSSKeyPermissionView}, wantVisible: true},
		{name: "should grant all but assign_group when the caller is in the key's group", caller: nssMember,
			want: []model.NSSKeyPermission{model.NSSKeyPermissionView, model.NSSKeyPermissionRotate, model.NSSKeyPermissionRevoke, model.NSSKeyPermissionEdit}, wantVisible: true},
		{name: "should hide the key when the caller holds no role and is not in its group", caller: nssPlain},
		{name: "should leave only view when the key is revoked and the caller is an admin", caller: nssAdmin, revoked: true,
			want: []model.NSSKeyPermission{model.NSSKeyPermissionView}, wantVisible: true},
		{name: "should leave only view when the key is revoked and the caller is in its group", caller: nssMember, revoked: true,
			want: []model.NSSKeyPermission{model.NSSKeyPermissionView}, wantVisible: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
			created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
			if tt.revoked {
				if _, err := keys.Revoke(t.Context(), nssAdmin, created.Key.ID, ""); err != nil {
					t.Fatalf("revoke: %v", err)
				}
			}

			view, err := keys.Get(t.Context(), tt.caller, created.Key.ID)
			if !tt.wantVisible {
				var notFound *errorresponses.NotFoundError
				if !errors.As(err, &notFound) {
					t.Fatalf("Get = %v, want a 404 for a key the caller may not view", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !reflect.DeepEqual(view.Permissions, tt.want) {
				t.Errorf("permissions = %v, want %v", view.Permissions, tt.want)
			}
		})
	}
}

func TestNSSKeys_ShouldListOnlyTheKeysTheCallersGroupsManageWhenListing(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	createNSSKey(t, keys, "web fleet", "web-admins")
	createNSSKey(t, keys, "db fleet", "db-admins")
	createNSSKey(t, keys, "bastions", "")

	tests := []struct {
		name   string
		caller *Identity
		want   []string
	}{
		{name: "should list nothing when the caller is an admin in no key's group", caller: nssAdmin, want: []string{}},
		{name: "should list nothing when the caller holds the key admin role in no key's group", caller: nssKeyAdmin, want: []string{}},
		{name: "should list nothing when the caller is SOC in no key's group", caller: nssSOC, want: []string{}},
		{name: "should list nothing when the caller is an auditor in no key's group", caller: nssAuditor, want: []string{}},
		{name: "should list only the group's key when the caller is in one key's group", caller: nssMember, want: []string{"web fleet"}},
		{name: "should list the group's key when an admin is also in that group", caller: &Identity{Subject: "sub-admin", Groups: []string{"admins", "db-admins"}}, want: []string{"db fleet"}},
		{name: "should list every group's keys by name when the caller is in several", caller: &Identity{Subject: "sub-member", Groups: []string{"web-admins", "db-admins"}}, want: []string{"db fleet", "web fleet"}},
		{name: "should list nothing when the caller holds no role and no key's group", caller: nssPlain, want: []string{}},
		{name: "should list nothing when the caller has no groups at all", caller: &Identity{Subject: "sub-none"}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			views, err := keys.List(t.Context(), tt.caller)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			got := []string{}
			for _, view := range views {
				got = append(got, view.Key.Name)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("keys = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNSSKeys_ShouldCreateAKeyWithItsDefaultsWhenOnlyANameIsGiven(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	ids := seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)

	view, secret, err := keys.Create(t.Context(), nssKeyAdmin, NSSKeyCreate{Name: "  web fleet  ", GroupName: "web-admins"}, "192.0.2.1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	key := view.Key
	if !regexp.MustCompile(`^nss_[0-9abcdefghjkmnpqrstvwxyz]{8}$`).MatchString(key.ID) {
		t.Errorf("id = %q, want nss_ and 8 lower-case Crockford base32 symbols", key.ID)
	}
	if !regexp.MustCompile(`^ssoossh_nss_[A-Za-z0-9_-]{43}$`).MatchString(secret) {
		t.Errorf("secret = %q, want ssoossh_nss_ and 32 bytes of unpadded base64url", secret)
	}
	if key.SecretHash != hashNSSSecret(secret) || len(key.SecretHash) != 64 {
		t.Errorf("stored hash = %q, want the hex SHA-256 of the secret", key.SecretHash)
	}
	if key.Name != "web fleet" || key.CurrentGeneration != 1 || key.CreatedBy != ids[nssKeyAdmin.Subject] {
		t.Errorf("key = %+v, want the trimmed name, generation 1 and the creator's users.id", key)
	}
	if key.ExpiresAt == nil || !key.ExpiresAt.Equal(nssNow.Add(8760*time.Hour)) {
		t.Errorf("expires_at = %v, want now plus nss.key_max_lifetime", key.ExpiresAt)
	}
	if key.NotificationEmail != "kim@example.org" {
		t.Errorf("notification_email = %q, want the creator's address", key.NotificationEmail)
	}
	if view.Status != model.NSSKeyStatusActive || view.CreatedByUsername != "kim" {
		t.Errorf("view = %+v, want active and created by kim", view)
	}

	details := auditDetails(t, db, AuditNSSKeyCreated)
	if len(details) != 1 || details[0]["key_id"] != key.ID {
		t.Fatalf("nss.key_created rows = %v, want one naming the key", details)
	}
	var raw []model.AuditEvent
	db.Find(&raw)
	for _, row := range raw {
		if strings.Contains(row.Payload, secret) || strings.Contains(row.Payload, key.SecretHash) {
			t.Errorf("audit row %s carries the secret or its hash", row.Action)
		}
	}
}

func TestNSSKeys_ShouldRefuseTheKeyWhenTheCreateRequestIsInvalid(t *testing.T) {
	t.Parallel()

	past := nssNow.Add(-time.Minute)
	tests := []struct {
		name    string
		caller  *Identity
		in      NSSKeyCreate
		wantErr func(error) bool
	}{
		{name: "should refuse the key when the caller is SOC", caller: nssSOC, in: NSSKeyCreate{Name: "x"}, wantErr: isForbidden},
		{name: "should refuse the key when the caller is in a key group only", caller: nssMember, in: NSSKeyCreate{Name: "x"}, wantErr: isForbidden},
		{name: "should refuse the key when the session has no users row", caller: &Identity{Subject: "sub-ghost", Groups: []string{"admins"}}, in: NSSKeyCreate{Name: "x"}, wantErr: isForbidden},
		{name: "should refuse the key when the name is blank", caller: nssAdmin, in: NSSKeyCreate{Name: "   "}, wantErr: isInvalid},
		{name: "should refuse the key when the name is over 100 characters", caller: nssAdmin, in: NSSKeyCreate{Name: strings.Repeat("n", 101)}, wantErr: isInvalid},
		{name: "should refuse the key when the description is over 1000 characters", caller: nssAdmin, in: NSSKeyCreate{Name: "x", Description: strings.Repeat("d", 1001)}, wantErr: isInvalid},
		{name: "should refuse the key when the expiry is in the past", caller: nssAdmin, in: NSSKeyCreate{Name: "x", ExpiresAt: &past}, wantErr: isInvalid},
		{name: "should refuse the key when the address is not an address", caller: nssAdmin, in: NSSKeyCreate{Name: "x", NotificationEmail: ptr("not an address")}, wantErr: isInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
			_, secret, err := keys.Create(t.Context(), tt.caller, tt.in, "")
			if !tt.wantErr(err) || secret != "" {
				t.Errorf("Create = %q, %v; want the refusal and no secret", secret, err)
			}
			var count int64
			db.Model(&model.NSSKey{}).Count(&count)
			if count != 0 {
				t.Errorf("keys stored = %d, want none", count)
			}
		})
	}
}

// isForbidden reports a 403.
func isForbidden(err error) bool {
	var forbidden *errorresponses.ForbiddenError
	return errors.As(err, &forbidden)
}

// isInvalid reports a 400.
func isInvalid(err error) bool {
	var invalid *errorresponses.InvalidRequestError
	return errors.As(err, &invalid)
}

// isConflict reports a 409.
func isConflict(err error) bool {
	var conflict *errorresponses.ConflictError
	return errors.As(err, &conflict)
}

// isNotFound reports a 404.
func isNotFound(err error) bool {
	var notFound *errorresponses.NotFoundError
	return errors.As(err, &notFound)
}

func TestNSSKeys_ShouldKeepTheDefaultsWhenTheCreateRequestSetsThem(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	expires := nssNow.Add(48 * time.Hour)

	view, _, err := keys.Create(t.Context(), nssAdmin, NSSKeyCreate{Name: "lab", ExpiresAt: &expires, NotificationEmail: ptr("")}, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if view.Key.ExpiresAt == nil || !view.Key.ExpiresAt.Equal(expires) || view.Key.NotificationEmail != "" {
		t.Errorf("key = %+v, want the given expiry and no address", view.Key)
	}
}

func TestNSSKeys_ShouldBoundTheExpiryWhenTheKeyIsCreated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		maxLifetime   time.Duration
		asked         *time.Time
		want          *time.Time
		wantRequested bool
	}{
		{name: "should take the ceiling when no expiry is given", maxLifetime: 8760 * time.Hour, want: nssAt(8760 * time.Hour)},
		{name: "should keep the expiry when it is inside the ceiling", maxLifetime: 8760 * time.Hour, asked: nssAt(48 * time.Hour), want: nssAt(48 * time.Hour)},
		{name: "should keep the expiry when it is the ceiling itself", maxLifetime: 8760 * time.Hour, asked: nssAt(8760 * time.Hour), want: nssAt(8760 * time.Hour)},
		{name: "should trim the expiry to the ceiling when it is later", maxLifetime: 8760 * time.Hour, asked: nssAt(8761 * time.Hour), want: nssAt(8760 * time.Hour), wantRequested: true},
		{name: "should never expire when the ceiling is 0 and no expiry is given", maxLifetime: 0, want: nil},
		{name: "should keep any future expiry when the ceiling is 0", maxLifetime: 0, asked: nssAt(87600 * time.Hour), want: nssAt(87600 * time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			cfg := nssLookupTestConfig()
			cfg.NSS.KeyMaxLifetime = tt.maxLifetime
			keys, _ := newTestNSSKeys(t, cfg, db)

			view, _, err := keys.Create(t.Context(), nssAdmin, NSSKeyCreate{Name: "lab", ExpiresAt: tt.asked}, "")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			var stored model.NSSKey
			if err := db.Where("id = ?", view.Key.ID).Take(&stored).Error; err != nil {
				t.Fatalf("read the key back: %v", err)
			}
			got := stored.ExpiresAt
			if (got == nil) != (tt.want == nil) || (got != nil && !got.Equal(*tt.want)) {
				t.Errorf("stored expires_at = %v, want %v", got, tt.want)
			}
			if view.Status != model.NSSKeyStatusActive {
				t.Errorf("status = %q, want active", view.Status)
			}
			details := auditDetails(t, db, AuditNSSKeyCreated)
			if len(details) != 1 {
				t.Fatalf("nss.key_created rows = %d, want 1", len(details))
			}
			if _, recorded := details[0]["requested_expires_at"]; recorded != tt.wantRequested {
				t.Errorf("requested_expires_at recorded = %v, want %v", recorded, tt.wantRequested)
			}
		})
	}
}

func TestNSSKeys_ShouldApplyTheChangeWhenTheCallerMayMakeIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		caller      *Identity
		in          NSSKeyUpdate
		wantErr     func(error) bool
		check       func(t *testing.T, key model.NSSKey)
		wantChanges []string
	}{
		{
			name: "should change the description when a group member edits it", caller: nssMember,
			in:          NSSKeyUpdate{Description: ptr("the web tier")},
			check:       func(t *testing.T, key model.NSSKey) { wantEqual(t, "description", key.Description, "the web tier") },
			wantChanges: []string{"description"},
		},
		{
			name: "should change the address when the key admin edits it", caller: nssKeyAdmin,
			in: NSSKeyUpdate{NotificationEmail: ptr(" team@example.org ")},
			check: func(t *testing.T, key model.NSSKey) {
				wantEqual(t, "notification_email", key.NotificationEmail, "team@example.org")
			},
			wantChanges: []string{"notification_email"},
		},
		{
			name: "should move the key to another group when the key admin assigns it", caller: nssKeyAdmin,
			in:          NSSKeyUpdate{GroupName: ptr("platform")},
			check:       func(t *testing.T, key model.NSSKey) { wantEqual(t, "group_name", key.GroupName, "platform") },
			wantChanges: []string{"group_name"},
		},
		{name: "should refuse a group change when a group member makes it", caller: nssMember, in: NSSKeyUpdate{GroupName: ptr("platform")}, wantErr: isForbidden},
		{name: "should refuse an edit when the caller is SOC", caller: nssSOC, in: NSSKeyUpdate{Description: ptr("x")}, wantErr: isForbidden},
		{name: "should refuse an edit when the caller is an auditor", caller: nssAuditor, in: NSSKeyUpdate{Description: ptr("x")}, wantErr: isForbidden},
		{name: "should hide the key when the caller may not view it", caller: nssPlain, in: NSSKeyUpdate{Description: ptr("x")}, wantErr: isNotFound},
		{name: "should refuse the change when nothing is sent", caller: nssAdmin, in: NSSKeyUpdate{}, wantErr: isInvalid},
		{name: "should refuse the change when the address is malformed", caller: nssAdmin, in: NSSKeyUpdate{NotificationEmail: ptr("@@")}, wantErr: isInvalid},
		{name: "should record nothing when the value is already stored", caller: nssAdmin, in: NSSKeyUpdate{GroupName: ptr("web-admins")}, wantChanges: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
			created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
			db.Model(&model.NSSKey{}).Where("id = ?", created.Key.ID).Update("expiry_reminder_sent_at", nssNow)

			view, err := keys.Update(t.Context(), tt.caller, created.Key.ID, tt.in, "192.0.2.9")
			if tt.wantErr != nil {
				if !tt.wantErr(err) {
					t.Fatalf("Update = %v, not the expected refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if tt.check != nil {
				tt.check(t, view.Key)
			}
			details := auditDetails(t, db, AuditNSSKeyUpdated)
			if tt.wantChanges == nil {
				if len(details) != 0 {
					t.Errorf("nss.key_updated rows = %v, want none for no change", details)
				}
				return
			}
			if len(details) != 1 {
				t.Fatalf("nss.key_updated rows = %d, want 1", len(details))
			}
			changes, _ := details[0]["changes"].(map[string]any)
			for _, field := range tt.wantChanges {
				if _, ok := changes[field]; !ok {
					t.Errorf("changes = %v, want %s recorded", changes, field)
				}
			}
		})
	}
}

// wantEqual fails unless got equals want.
func wantEqual[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func TestNSSKeys_ShouldIssueANewSecretAndKeepThePreviousOneWhenRotated(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, notifier := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, oldSecret := createNSSKey(t, keys, "web fleet", "web-admins")
	db.Model(&model.NSSKey{}).Where("id = ?", created.Key.ID).Update("rotation_cleanup_sent_at", nssNow)

	view, newSecret, err := keys.Rotate(t.Context(), nssMember, created.Key.ID, "192.0.2.9")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	key := view.Key
	if newSecret == oldSecret || !strings.HasPrefix(newSecret, nssKeySecretPrefix) {
		t.Errorf("new secret = %q, want a fresh one", newSecret)
	}
	if key.CurrentGeneration != 2 || key.SecretHash != hashNSSSecret(newSecret) {
		t.Errorf("key = %+v, want generation 2 with the new hash", key)
	}
	if key.PreviousSecretHash == nil || *key.PreviousSecretHash != hashNSSSecret(oldSecret) {
		t.Errorf("previous hash = %v, want the old secret's", key.PreviousSecretHash)
	}
	if key.PreviousExpiresAt == nil || !key.PreviousExpiresAt.Equal(nssNow.Add(720*time.Hour)) {
		t.Errorf("previous_expires_at = %v, want now plus nss.key_rotation_grace", key.PreviousExpiresAt)
	}
	if key.RotatedAt == nil || !key.RotatedAt.Equal(nssNow) || key.RotationCleanupSentAt != nil {
		t.Errorf("key = %+v, want rotated now with the cleanup claim cleared", key)
	}

	for _, tc := range []struct {
		secret string
		want   int
	}{{newSecret, 2}, {oldSecret, 1}} {
		verdict, err := keys.VerifyFleetKey(t.Context(), key.ID, tc.secret)
		if err != nil || !verdict.OK || verdict.Generation != tc.want {
			t.Errorf("verify generation %d = %+v, %v", tc.want, verdict, err)
		}
	}

	event := notifier.only(t, notify.KindNSSKeyRotated)
	payload, ok := event.Payload.(*notify.NSSKeyRotated)
	if event.NSSKeyID != key.ID || !ok || payload.Generation != 2 || payload.RotatedByUsername != "mo" ||
		!payload.PreviousExpiresAt.Equal(*key.PreviousExpiresAt) {
		t.Errorf("rotated mail = %+v %+v, want it addressed to the key with the rotation", event, payload)
	}
	if details := auditDetails(t, db, AuditNSSKeyRotated); len(details) != 1 || details[0]["generation"] != float64(2) {
		t.Errorf("nss.key_rotated rows = %v, want one at generation 2", details)
	}
}

func TestNSSKeys_ShouldRenewTheExpiryWhenTheKeyIsRotated(t *testing.T) {
	t.Parallel()

	const day = 24 * time.Hour
	tests := []struct {
		name      string
		limit     time.Duration
		created   time.Duration // before now
		rotated   *time.Time
		expires   *time.Time
		want      *time.Time
		wantGrace bool
	}{
		{name: "should renew by the key's own lifetime when it is inside the limit", limit: 365 * day, created: 10 * day, expires: nssAt(20 * day), want: nssAt(30 * day), wantGrace: true},
		{name: "should measure the lifetime from the last rotation when there was one", limit: 365 * day, created: 400 * day, rotated: nssAt(-10 * day), expires: nssAt(20 * day), want: nssAt(30 * day), wantGrace: true},
		{name: "should cap the renewal at the limit when the key's lifetime is longer", limit: 30 * day, created: 10 * day, expires: nssAt(355 * day), want: nssAt(30 * day), wantGrace: true},
		{name: "should give a key that never expired the limit when one is set", limit: 365 * day, created: 10 * day, want: nssAt(365 * day), wantGrace: true},
		{name: "should keep a key that never expires when there is no limit", limit: 0, created: 10 * day, want: nil, wantGrace: true},
		{name: "should renew by the key's own lifetime when there is no limit", limit: 0, created: 10 * day, expires: nssAt(20 * day), want: nssAt(30 * day), wantGrace: true},
		{name: "should renew an expired key and give its old secret no grace", limit: 365 * day, created: 40 * day, expires: nssAt(-10 * day), want: nssAt(30 * day), wantGrace: false},
		{name: "should keep the expiry when it precedes the key's start and there is no limit", limit: 0, created: day, expires: nssAt(-2 * day), want: nssAt(-2 * day), wantGrace: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			cfg := nssLookupTestConfig()
			cfg.NSS.KeyMaxLifetime = tt.limit
			keys, notifier := newTestNSSKeys(t, cfg, db)
			created, oldSecret := createNSSKey(t, keys, "web fleet", "web-admins")
			if err := db.Model(&model.NSSKey{}).Where("id = ?", created.Key.ID).Updates(map[string]any{
				"created_at":              nssNow.Add(-tt.created),
				"rotated_at":              tt.rotated,
				"expires_at":              tt.expires,
				"expiry_reminder_sent_at": nssNow,
			}).Error; err != nil {
				t.Fatalf("shape the key: %v", err)
			}

			view, _, err := keys.Rotate(t.Context(), nssKeyAdmin, created.Key.ID, "")
			if err != nil {
				t.Fatalf("Rotate: %v", err)
			}
			key := view.Key
			if (key.ExpiresAt == nil) != (tt.want == nil) || (key.ExpiresAt != nil && !key.ExpiresAt.Equal(*tt.want)) {
				t.Errorf("expires_at = %v, want %v", key.ExpiresAt, tt.want)
			}
			if key.ExpiryReminderSentAt != nil {
				t.Errorf("expiry_reminder_sent_at = %v, want the reminders restarted", key.ExpiryReminderSentAt)
			}
			verdict, err := keys.VerifyFleetKey(t.Context(), key.ID, oldSecret)
			if err != nil || verdict.OK != tt.wantGrace {
				t.Errorf("old secret accepted = %v (%v), want %v", verdict.OK, err, tt.wantGrace)
			}
			payload, _ := notifier.only(t, notify.KindNSSKeyRotated).Payload.(*notify.NSSKeyRotated)
			if payload == nil || !payload.ExpiresAt.Equal(derefTime(tt.want)) {
				t.Errorf("rotated mail = %+v, want the renewed expiry", payload)
			}
			details := auditDetails(t, db, AuditNSSKeyRotated)
			if _, recorded := details[0]["expires_at"]; len(details) != 1 || !recorded {
				t.Errorf("nss.key_rotated rows = %v, want one carrying the renewed expiry", details)
			}
		})
	}
}

func TestNSSKeys_ShouldRefuseTheRotationWhenTheCallerMayNotRotate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		caller  *Identity
		revoked bool
		wantErr func(error) bool
	}{
		{name: "should refuse the rotation when the caller is SOC", caller: nssSOC, wantErr: isForbidden},
		{name: "should refuse the rotation when the caller is an auditor", caller: nssAuditor, wantErr: isForbidden},
		{name: "should hide the key when the caller may not view it", caller: nssPlain, wantErr: isNotFound},
		{name: "should refuse the rotation when the key is revoked", caller: nssAdmin, revoked: true, wantErr: isConflict},
		{name: "should hide an unknown key", caller: nssAdmin, wantErr: isNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, notifier := newTestNSSKeys(t, nssLookupTestConfig(), db)
			created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
			id := created.Key.ID
			if tt.revoked {
				if _, err := keys.Revoke(t.Context(), nssAdmin, id, ""); err != nil {
					t.Fatalf("revoke: %v", err)
				}
			}
			if strings.Contains(tt.name, "unknown") {
				id = "nss_00000000"
			}
			view, secret, err := keys.Rotate(t.Context(), tt.caller, id, "")
			if !tt.wantErr(err) || view != nil || secret != "" {
				t.Errorf("Rotate = %v, %q, %v; want the refusal and no secret", view, secret, err)
			}
			for _, event := range notifier.captured() {
				if event.Kind == notify.KindNSSKeyRotated {
					t.Errorf("a refused rotation mailed %+v", event)
				}
			}
		})
	}
}

func TestNSSKeys_ShouldEndThePreviousSecretWhenItIsClearedEarly(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, oldSecret := createNSSKey(t, keys, "web fleet", "web-admins")

	if _, err := keys.ClearPrevious(t.Context(), nssKeyAdmin, created.Key.ID, ""); !isConflict(err) {
		t.Errorf("ClearPrevious before any rotation = %v, want 409", err)
	}
	if _, _, err := keys.Rotate(t.Context(), nssKeyAdmin, created.Key.ID, ""); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := keys.ClearPrevious(t.Context(), nssSOC, created.Key.ID, ""); !isForbidden(err) {
		t.Errorf("ClearPrevious by SOC = %v, want 403", err)
	}
	view, err := keys.ClearPrevious(t.Context(), nssMember, created.Key.ID, "")
	if err != nil {
		t.Fatalf("ClearPrevious: %v", err)
	}
	if view.Key.PreviousSecretHash != nil || view.Key.PreviousExpiresAt != nil {
		t.Errorf("key = %+v, want no previous secret", view.Key)
	}
	if verdict, _ := keys.VerifyFleetKey(t.Context(), created.Key.ID, oldSecret); verdict.OK {
		t.Error("the previous secret still verifies after it was cleared")
	}
	if details := auditDetails(t, db, AuditNSSKeyPreviousCleared); len(details) != 1 {
		t.Errorf("nss.key_previous_cleared rows = %d, want 1", len(details))
	}
}

func TestNSSKeys_ShouldEndTheKeyForGoodWhenItIsRevoked(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	ids := seedNSSCallers(t, db)
	keys, notifier := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, secret := createNSSKey(t, keys, "web fleet", "web-admins")

	if _, err := keys.Revoke(t.Context(), nssAuditor, created.Key.ID, ""); !isForbidden(err) {
		t.Errorf("Revoke by an auditor = %v, want 403", err)
	}
	view, err := keys.Revoke(t.Context(), nssSOC, created.Key.ID, "192.0.2.9")
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if view.Status != model.NSSKeyStatusRevoked || view.Key.RevokedBy == nil || *view.Key.RevokedBy != ids[nssSOC.Subject] ||
		view.RevokedByUsername != "sam" {
		t.Errorf("view = %+v, want revoked by the SOC caller", view)
	}
	if _, err := keys.Revoke(t.Context(), nssAdmin, created.Key.ID, ""); !isConflict(err) {
		t.Errorf("a second Revoke = %v, want 409", err)
	}
	if _, err := keys.Update(t.Context(), nssAdmin, created.Key.ID, NSSKeyUpdate{Description: ptr("x")}, ""); !isConflict(err) {
		t.Errorf("Update of a revoked key = %v, want 409", err)
	}
	verdict, err := keys.VerifyFleetKey(t.Context(), created.Key.ID, secret)
	if err != nil || verdict.OK || verdict.Dead != NSSDeadKeyRevoked {
		t.Errorf("verify a revoked key = %+v, %v; want refused as revoked", verdict, err)
	}
	event := notifier.only(t, notify.KindNSSKeyRevoked)
	if payload, ok := event.Payload.(*notify.NSSKeyRevoked); !ok || event.NSSKeyID != created.Key.ID || payload.RevokedByUsername != "sam" {
		t.Errorf("revoked mail = %+v, want it addressed to the key naming sam", event)
	}
	if details := auditDetails(t, db, AuditNSSKeyRevoked); len(details) != 1 {
		t.Errorf("nss.key_revoked rows = %d, want 1", len(details))
	}
}

func TestNSSKeys_ShouldVerifyOnlyALiveKeysSecretsWhenAKeyIsPresented(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(t *testing.T, db *gorm.DB, keys *NSSKeyService, id string)
		present    func(current, previous string) (id, secret string)
		wantOK     bool
		wantGen    int
		wantDead   string
		wantNoKey  bool
		keyIDValue string
	}{
		{name: "should accept the current secret", present: func(c, _ string) (string, string) { return "", c }, wantOK: true, wantGen: 2},
		{name: "should accept the previous secret inside its grace", present: func(_, p string) (string, string) { return "", p }, wantOK: true, wantGen: 1},
		{
			name: "should refuse the previous secret once its grace has ended",
			setup: func(t *testing.T, db *gorm.DB, _ *NSSKeyService, id string) {
				db.Model(&model.NSSKey{}).Where("id = ?", id).Update("previous_expires_at", nssNow.Add(-time.Second))
			},
			present: func(_, p string) (string, string) { return "", p },
		},
		{name: "should refuse a wrong secret", present: func(string, string) (string, string) { return "", "ssoossh_nss_wrong" }},
		{name: "should refuse an empty secret", present: func(string, string) (string, string) { return "", "" }},
		{name: "should refuse an unknown key id", present: func(c, _ string) (string, string) { return "nss_zzzzzzzz", c }, wantNoKey: true},
		{name: "should refuse an empty key id", present: func(c, _ string) (string, string) { return "", c }, keyIDValue: "-", wantNoKey: true},
		{
			name: "should refuse a revoked key even with its current secret",
			setup: func(t *testing.T, _ *gorm.DB, keys *NSSKeyService, id string) {
				if _, err := keys.Revoke(t.Context(), nssAdmin, id, ""); err != nil {
					t.Fatalf("revoke: %v", err)
				}
			},
			present:  func(c, _ string) (string, string) { return "", c },
			wantDead: NSSDeadKeyRevoked,
		},
		{
			name: "should refuse an expired key even with its current secret",
			setup: func(t *testing.T, db *gorm.DB, _ *NSSKeyService, id string) {
				db.Model(&model.NSSKey{}).Where("id = ?", id).Update("expires_at", nssNow)
			},
			present:  func(c, _ string) (string, string) { return "", c },
			wantDead: NSSDeadKeyExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
			created, previous := createNSSKey(t, keys, "web fleet", "web-admins")
			_, current, err := keys.Rotate(t.Context(), nssAdmin, created.Key.ID, "")
			if err != nil {
				t.Fatalf("rotate: %v", err)
			}
			if tt.setup != nil {
				tt.setup(t, db, keys, created.Key.ID)
			}
			id, secret := tt.present(current, previous)
			switch {
			case tt.keyIDValue == "-":
				id = ""
			case id == "":
				id = created.Key.ID
			}

			verdict, err := keys.VerifyFleetKey(t.Context(), id, secret)
			if err != nil {
				t.Fatalf("VerifyFleetKey: %v", err)
			}
			if verdict.OK != tt.wantOK || verdict.Generation != tt.wantGen || verdict.Dead != tt.wantDead {
				t.Errorf("verdict = %+v, want ok %v generation %d dead %q", verdict, tt.wantOK, tt.wantGen, tt.wantDead)
			}
			if (verdict.Key == nil) != tt.wantNoKey {
				t.Errorf("verdict key = %v, want present %v", verdict.Key, !tt.wantNoKey)
			}
			if verdict.KeyID != id {
				t.Errorf("verdict key id = %q, want the presented %q", verdict.KeyID, id)
			}
		})
	}
}

func TestNSSKeys_ShouldReturnTheDatabaseErrorWhenVerificationCannotRead(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	_ = sqlDB.Close()
	if _, err := keys.VerifyFleetKey(t.Context(), "nss_abcdefgh", "s"); err == nil {
		t.Error("VerifyFleetKey on a closed database succeeded, want the error")
	}
}

func TestNSSKeys_ShouldDeriveTheStatusWhenTheKeyIsRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  model.NSSKey
		want model.NSSKeyStatus
	}{
		{name: "should be active when the key is live", key: model.NSSKey{ExpiresAt: nssAt(time.Hour)}, want: model.NSSKeyStatusActive},
		{name: "should be active when the key never expires", key: model.NSSKey{}, want: model.NSSKeyStatusActive},
		{name: "should be expired when expires_at is now", key: model.NSSKey{ExpiresAt: nssAt(0)}, want: model.NSSKeyStatusExpired},
		{name: "should be revoked when it is revoked, expired or not", key: model.NSSKey{ExpiresAt: nssAt(-time.Hour), RevokedAt: nssAt(-time.Hour)}, want: model.NSSKeyStatusRevoked},
		{name: "should be revoked when a key that never expires is revoked", key: model.NSSKey{RevokedAt: nssAt(-time.Hour)}, want: model.NSSKeyStatusRevoked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := nssKeyStatus(&tt.key, nssNow); got != tt.want {
				t.Errorf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNSSKeys_ShouldDrawAnotherIDWhenTheFirstIsTaken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ids     []string
		wantID  string
		wantErr bool
	}{
		{name: "should use the second id when the first collides", ids: []string{"nss_taken000", "nss_fresh000"}, wantID: "nss_fresh000"},
		{name: "should give up when three ids in a row collide", ids: []string{"nss_taken000", "nss_taken000", "nss_taken000"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
			keys.newID = func() (string, error) { return "nss_taken000", nil }
			createNSSKey(t, keys, "first", "")
			draws := 0
			keys.newID = func() (string, error) {
				id := tt.ids[draws]
				draws++
				return id, nil
			}

			view, _, err := keys.Create(t.Context(), nssAdmin, NSSKeyCreate{Name: "second"}, "")
			if tt.wantErr {
				if err == nil {
					t.Errorf("Create = %+v, want an error after three collisions", view)
				}
				return
			}
			if err != nil || view.Key.ID != tt.wantID || draws != 2 {
				t.Errorf("Create = %v, %v after %d draws; want %s on the second", view, err, draws, tt.wantID)
			}
		})
	}
}

// seedNSSAlert writes one alert claim, as the usage service's raise would.
func seedNSSAlert(t *testing.T, db *gorm.DB, kind, subject string, at time.Time) {
	t.Helper()

	if err := db.Create(&model.NSSAlert{Kind: kind, Subject: subject, LastRaisedAt: at.UTC()}).Error; err != nil {
		t.Fatalf("seed alert: %v", err)
	}
}

// timeOrNil renders an optional time for a failure message.
func timeOrNil(at *time.Time) string {
	if at == nil {
		return "nil"
	}
	return at.UTC().Format(time.RFC3339)
}

// sameTime reports whether two optional times are both nil or the same
// instant.
func sameTime(got, want *time.Time) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return got.Equal(*want)
}

func TestNSSKeys_ShouldReportTheLatestUseWhenTheKeyIsRead(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	used, _ := createNSSKey(t, keys, "web fleet", "web-admins")
	unused, _ := createNSSKey(t, keys, "db fleet", "web-admins")
	single, _ := createNSSKey(t, keys, "bastions", "web-admins")

	// The used key's latest use is on an older generation from a second
	// address, a day after the newer row's day: the newest instant wins,
	// not the newest day or generation.
	latest := nssNow.Add(-2 * time.Hour)
	seedNSSUsage(t, db, used.Key.ID, "192.0.2.1", 2, nssNow.Add(-30*time.Hour), nssNow.Add(-26*time.Hour), 40)
	seedNSSUsage(t, db, used.Key.ID, "192.0.2.2", 1, nssNow.Add(-5*time.Hour), latest, 3)
	seedNSSUsage(t, db, used.Key.ID, "192.0.2.3", 2, nssNow.Add(-50*time.Hour), nssNow.Add(-49*time.Hour), 9)
	singleSeen := nssNow.Add(-72 * time.Hour)
	seedNSSUsage(t, db, single.Key.ID, "192.0.2.9", 1, singleSeen.Add(-time.Hour), singleSeen, 1)

	tests := []struct {
		name string
		id   string
		want *time.Time
	}{
		{name: "should report the newest last seen when the key was used from several addresses", id: used.Key.ID, want: &latest},
		{name: "should report nothing when no host has used the key", id: unused.Key.ID},
		{name: "should report the one row's last seen when the key was used once", id: single.Key.ID, want: &singleSeen},
	}
	views, err := keys.List(t.Context(), nssMember)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	listed := map[string]*time.Time{}
	for _, view := range views {
		listed[view.Key.ID] = view.LastUsedAt
	}
	for _, tt := range tests {
		t.Run(tt.name+" in the list", func(t *testing.T) {
			if got := listed[tt.id]; !sameTime(got, tt.want) {
				t.Errorf("listed last used = %s, want %s", timeOrNil(got), timeOrNil(tt.want))
			}
		})
		t.Run(tt.name+" on its own", func(t *testing.T) {
			view, err := keys.Get(t.Context(), nssMember, tt.id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !sameTime(view.LastUsedAt, tt.want) {
				t.Errorf("last used = %s, want %s", timeOrNil(view.LastUsedAt), timeOrNil(tt.want))
			}
		})
	}
}

func TestNSSKeys_ShouldKeepTheLatestUseWhenTheKeyIsChanged(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
	lastSeen := nssNow.Add(-time.Hour)
	seedNSSUsage(t, db, created.Key.ID, "192.0.2.1", 1, nssNow.Add(-3*time.Hour), lastSeen, 5)

	description := "every host in the web tier"
	view, err := keys.Update(t.Context(), nssMember, created.Key.ID, NSSKeyUpdate{Description: &description}, "")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !sameTime(view.LastUsedAt, &lastSeen) {
		t.Errorf("last used after the change = %s, want %s", timeOrNil(view.LastUsedAt), timeOrNil(&lastSeen))
	}
}

func TestNSSKeys_ShouldReportTheRecentUsageAlertsWhenTheKeyIsRead(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	both, _ := createNSSKey(t, keys, "web fleet", "web-admins")
	stale, _ := createNSSKey(t, keys, "db fleet", "web-admins")
	quiet, _ := createNSSKey(t, keys, "bastions", "web-admins")
	edge, _ := createNSSKey(t, keys, "build fleet", "web-admins")

	missAt := nssNow.Add(-time.Hour)
	sourceAt := nssNow.Add(-50 * time.Hour)
	edgeAt := nssNow.Add(-NSSKeyAlertLookback)
	seedNSSAlert(t, db, nssAlertMissRate, both.Key.ID, missAt)
	seedNSSAlert(t, db, nssAlertKeySource, both.Key.ID, sourceAt)
	// Older than the lookback: not reported.
	seedNSSAlert(t, db, nssAlertMissRate, stale.Key.ID, nssNow.Add(-NSSKeyAlertLookback-time.Minute))
	seedNSSAlert(t, db, nssAlertKeySource, stale.Key.ID, nssNow.Add(-30*24*time.Hour))
	// Other kinds about the quiet key, and a usage alert about something
	// that is not a key id: none is a usage alert for a key.
	seedNSSAlert(t, db, nssAlertRevokedAttempt, quiet.Key.ID, missAt)
	seedNSSAlert(t, db, nssAlertExpiredAttempt, quiet.Key.ID, missAt)
	seedNSSAlert(t, db, nssAlertPrivilegedName, quiet.Key.ID+"|root", missAt)
	// Exactly at the lookback's edge: reported.
	seedNSSAlert(t, db, nssAlertKeySource, edge.Key.ID, edgeAt)

	tests := []struct {
		name       string
		id         string
		wantMiss   *time.Time
		wantSource *time.Time
	}{
		{name: "should report both alerts when both fired this week", id: both.Key.ID, wantMiss: &missAt, wantSource: &sourceAt},
		{name: "should report neither alert when both fired before the last 7 days", id: stale.Key.ID},
		{name: "should report neither alert when only other kinds were raised about the key", id: quiet.Key.ID},
		{name: "should report the alert when it fired exactly 7 days ago", id: edge.Key.ID, wantSource: &edgeAt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view, err := keys.Get(t.Context(), nssAuditor, tt.id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !sameTime(view.MissRateAlertAt, tt.wantMiss) || !sameTime(view.KeySourceAlertAt, tt.wantSource) {
				t.Errorf("alerts = miss rate %s, key source %s; want %s, %s",
					timeOrNil(view.MissRateAlertAt), timeOrNil(view.KeySourceAlertAt), timeOrNil(tt.wantMiss), timeOrNil(tt.wantSource))
			}
		})
	}
}

func TestNSSKeys_ShouldReturnTheConfiguredLifetimesWhenTheSettingsAreRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		grace time.Duration
		want  NSSKeySettings
	}{
		{name: "should return both durations when the grace is set", grace: 720 * time.Hour, want: NSSKeySettings{MaxLifetime: 8760 * time.Hour, RotationGrace: 720 * time.Hour}},
		{name: "should return a zero grace when rotation ends the previous secret at once", grace: 0, want: NSSKeySettings{MaxLifetime: 8760 * time.Hour}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := nssLookupTestConfig()
			c.NSS.KeyRotationGrace = tt.grace
			keys, err := NewNSSKeyService(c, nil, nil)
			if err != nil {
				t.Fatalf("NewNSSKeyService: %v", err)
			}
			if got := keys.Settings(); got != tt.want {
				t.Errorf("Settings = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNSSKeys_ShouldReturnTheDatabaseErrorWhenTheKeysActivityCannotBeRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		table string
	}{
		{name: "should fail the list when the usage table cannot be read", table: "nss_key_usage"},
		{name: "should fail the list when the alert claims cannot be read", table: "nss_alerts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
			createNSSKey(t, keys, "web fleet", "web-admins")
			if err := db.Exec("DROP TABLE " + tt.table).Error; err != nil {
				t.Fatalf("drop %s: %v", tt.table, err)
			}
			if views, err := keys.List(t.Context(), nssMember); err == nil {
				t.Errorf("List = %d keys, want the error reading %s", len(views), tt.table)
			}
		})
	}
}
