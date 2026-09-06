package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// newIdentityTestDB gives the identity tests the two tables a refresh
// reads: the users row (the OIDC half) and the directory bookkeeping row
// (the overlay).
func newIdentityTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.UserLDAP{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	return db
}

// seedIdentityUser writes one users row and returns its id.
func seedIdentityUser(t *testing.T, db *gorm.DB, subject string, other, service []string, extra map[string]string) string {
	t.Helper()

	otherJSON, err := json.Marshal(other)
	if err != nil {
		t.Fatalf("failed to encode other accounts: %v", err)
	}
	serviceJSON, err := json.Marshal(service)
	if err != nil {
		t.Fatalf("failed to encode service accounts: %v", err)
	}
	extraJSON := "{}"
	if extra != nil {
		encoded, err := json.Marshal(extra)
		if err != nil {
			t.Fatalf("failed to encode extra fields: %v", err)
		}
		extraJSON = string(encoded)
	}

	now := time.Now()
	user := model.User{
		ID:              "user-" + subject,
		Subject:         subject,
		Username:        "alice",
		Email:           "alice@example.com",
		OtherAccounts:   string(otherJSON),
		ServiceAccounts: string(serviceJSON),
		ExtraFields:     extraJSON,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed the users row: %v", err)
	}
	return user.ID
}

// seedUserLDAP writes the directory bookkeeping row holding values.
func seedUserLDAP(t *testing.T, db *gorm.DB, userID string, values map[string][]string) {
	t.Helper()

	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("failed to encode directory values: %v", err)
	}
	now := time.Now()
	row := model.UserLDAP{
		UserID:     userID,
		DN:         "uid=alice,ou=People,dc=example,dc=net",
		Attributes: string(encoded),
		LastSeenAt: &now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("failed to seed the user_ldap row: %v", err)
	}
}

// enabledLDAPService builds a directory service with the fields the tests
// exercise. No dialer is ever used: a refresh reads the stored row only.
func enabledLDAPService(t *testing.T, db *gorm.DB) *LDAPService {
	t.Helper()

	cfg := &config.Config{LDAP: config.LDAPConfig{
		Enabled:    true,
		URL:        "ldaps://dir.example.net",
		BaseDN:     "dc=example,dc=net",
		UserFilter: "(uid={{.Username}})",
		Fields: map[string]config.LDAPField{
			config.LDAPFieldOtherAccounts:   {Attribute: "altSecurityIdentities"},
			config.LDAPFieldServiceAccounts: {Attribute: "owns"},
			"employee_type":                 {Attribute: "employeeType"},
		},
	}}
	svc, err := NewLDAPService(cfg, db)
	if err != nil {
		t.Fatalf("failed to build the directory service: %v", err)
	}
	return svc
}

// TestRefreshIdentity_ShouldReplaceAccountListsWithWhatIsStored is the
// regression this exists for: the session cookie freezes the login-time
// merge, so a directory sync that removed an account left a live session
// still offering it as a certificate principal.
func TestRefreshIdentity_ShouldReplaceAccountListsWithWhatIsStored(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// session is what the cookie carries — the login-time snapshot.
		session *Identity
		// stored are the directory values the sync last wrote.
		stored          map[string][]string
		wantOther       []string
		wantService     []string
		wantGroups      []string
		wantEmployeeTyp string
	}{
		{
			name: "should drop an account the directory sync took away",
			session: &Identity{
				Subject:         "sub-alice",
				OtherAccounts:   []string{"alice.adm", "shared-ops"},
				ServiceAccounts: []string{"svc-deploy"},
			},
			stored:      map[string][]string{"other_accounts": {"alice.adm"}, "service_accounts": {"svc-deploy"}},
			wantOther:   []string{"alice.adm"},
			wantService: []string{"svc-deploy"},
		},
		{
			name: "should drop a service account the directory sync took away",
			session: &Identity{
				Subject:         "sub-alice",
				ServiceAccounts: []string{"svc-deploy", "svc-retired"},
			},
			stored:      map[string][]string{"service_accounts": {"svc-deploy"}},
			wantOther:   []string{},
			wantService: []string{"svc-deploy"},
		},
		{
			name: "should pick up an account the directory sync added",
			session: &Identity{
				Subject:       "sub-alice",
				OtherAccounts: []string{"alice.adm"},
			},
			stored:      map[string][]string{"other_accounts": {"alice.adm", "alice.svc"}},
			wantOther:   []string{"alice.adm", "alice.svc"},
			wantService: []string{},
		},
		{
			name: "should empty a list the directory now resolves to nothing",
			session: &Identity{
				Subject:         "sub-alice",
				ServiceAccounts: []string{"svc-deploy"},
			},
			stored:      map[string][]string{"service_accounts": {}},
			wantOther:   []string{},
			wantService: []string{},
		},
		{
			name: "should keep the group claim the session was issued with",
			session: &Identity{
				Subject: "sub-alice",
				Groups:  []string{"ssh-admins"},
			},
			stored:      map[string][]string{"groups": {"ssh-users"}},
			wantOther:   []string{},
			wantService: []string{},
			wantGroups:  []string{"ssh-admins"},
		},
		{
			name:            "should apply a directory extra field",
			session:         &Identity{Subject: "sub-alice"},
			stored:          map[string][]string{"employee_type": {"contractor"}},
			wantOther:       []string{},
			wantService:     []string{},
			wantEmployeeTyp: "contractor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := newIdentityTestDB(t)
			userID := seedIdentityUser(t, db, tt.session.Subject, []string{}, []string{}, nil)
			seedUserLDAP(t, db, userID, tt.stored)

			svc := NewIdentityService(db, enabledLDAPService(t, db))
			if err := svc.RefreshIdentity(context.Background(), tt.session); err != nil {
				t.Fatalf("RefreshIdentity() returned %v, want no error", err)
			}

			if !equalStrings(orEmptyStrings(tt.session.OtherAccounts), tt.wantOther) {
				t.Errorf("got other accounts %v, want %v", tt.session.OtherAccounts, tt.wantOther)
			}
			if !equalStrings(orEmptyStrings(tt.session.ServiceAccounts), tt.wantService) {
				t.Errorf("got service accounts %v, want %v", tt.session.ServiceAccounts, tt.wantService)
			}
			if tt.wantGroups != nil && !equalStrings(tt.session.Groups, tt.wantGroups) {
				t.Errorf("got groups %v, want %v", tt.session.Groups, tt.wantGroups)
			}
			if tt.wantEmployeeTyp != "" {
				got, _ := tt.session.Extra["employee_type"].Scalar()
				if got != tt.wantEmployeeTyp {
					t.Errorf("got employee_type %q, want %q", got, tt.wantEmployeeTyp)
				}
			}
		})
	}
}

// TestRefreshIdentity_ShouldFallBackToTheOIDCValuesWithoutADirectory covers
// the LDAP-disabled deployment: the users row is the whole answer, and the
// refresh still has to produce it rather than leaving the cookie's copy.
func TestRefreshIdentity_ShouldFallBackToTheOIDCValuesWithoutADirectory(t *testing.T) {
	t.Parallel()

	db := newIdentityTestDB(t)
	seedIdentityUser(t, db, "sub-alice", []string{"alice.adm"}, []string{"svc-deploy"}, map[string]string{"employee_id": "E-1"})

	identity := &Identity{Subject: "sub-alice", OtherAccounts: []string{"stale"}, ServiceAccounts: []string{"stale"}}
	svc := NewIdentityService(db, nil)
	if err := svc.RefreshIdentity(context.Background(), identity); err != nil {
		t.Fatalf("RefreshIdentity() returned %v, want no error", err)
	}

	if !equalStrings(identity.OtherAccounts, []string{"alice.adm"}) {
		t.Errorf("got other accounts %v, want [alice.adm]", identity.OtherAccounts)
	}
	if !equalStrings(identity.ServiceAccounts, []string{"svc-deploy"}) {
		t.Errorf("got service accounts %v, want [svc-deploy]", identity.ServiceAccounts)
	}
	if got, _ := identity.Extra["employee_id"].Scalar(); got != "E-1" {
		t.Errorf("got employee_id %q, want %q", got, "E-1")
	}
}

// TestRefreshIdentity_ShouldLeaveTheSessionAloneWhenThereIsNoUsersRow keeps
// a deleted or not-yet-written row from emptying a live session's
// principals, which would fail an approval that has nothing wrong with it.
func TestRefreshIdentity_ShouldLeaveTheSessionAloneWhenThereIsNoUsersRow(t *testing.T) {
	t.Parallel()

	db := newIdentityTestDB(t)
	identity := &Identity{Subject: "sub-nobody", OtherAccounts: []string{"alice.adm"}}

	svc := NewIdentityService(db, nil)
	if err := svc.RefreshIdentity(context.Background(), identity); err != nil {
		t.Fatalf("RefreshIdentity() returned %v, want no error", err)
	}

	if !equalStrings(identity.OtherAccounts, []string{"alice.adm"}) {
		t.Errorf("got other accounts %v, want the session values [alice.adm]", identity.OtherAccounts)
	}
}

// TestRefreshIdentity_ShouldIgnoreANilServiceOrIdentity covers the guards
// that let callers skip the nil checks at every call site.
func TestRefreshIdentity_ShouldIgnoreANilServiceOrIdentity(t *testing.T) {
	t.Parallel()

	var nilService *IdentityService
	if err := nilService.RefreshIdentity(context.Background(), &Identity{Subject: "sub-alice"}); err != nil {
		t.Errorf("RefreshIdentity() on a nil service returned %v, want no error", err)
	}

	svc := NewIdentityService(newIdentityTestDB(t), nil)
	if err := svc.RefreshIdentity(context.Background(), nil); err != nil {
		t.Errorf("RefreshIdentity() with a nil identity returned %v, want no error", err)
	}
}

// TestEffectiveExtra_ShouldRenderBothValueShapes checks the wire conversion
// the account page reads, including the empty case it must not return nil
// for.
func TestEffectiveExtra_ShouldRenderBothValueShapes(t *testing.T) {
	t.Parallel()

	identity := &Identity{Extra: map[string]extraValue{
		"employee_id": scalarExtra("E-1"),
		"teams":       listExtra([]string{"team-a", "team-b"}),
	}}

	got := identity.EffectiveExtra()

	if got["employee_id"] != "E-1" {
		t.Errorf("got employee_id %v, want E-1", got["employee_id"])
	}
	teams, ok := got["teams"].([]string)
	if !ok || !equalStrings(teams, []string{"team-a", "team-b"}) {
		t.Errorf("got teams %v, want [team-a team-b]", got["teams"])
	}

	empty := (&Identity{}).EffectiveExtra()
	if empty == nil {
		t.Error("EffectiveExtra() returned nil, want an empty map")
	}
}

// orEmptyStrings normalizes nil to an empty slice so a test can compare the
// two the same way.
func orEmptyStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// equalStrings compares two string slices element by element.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
