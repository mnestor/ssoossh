package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/test/testdb"
)

// wantNSSCode fails unless err is an NSSError carrying code.
func wantNSSCode(t *testing.T, err error, code string) {
	t.Helper()

	var nssErr *errorresponses.NSSError
	if !errors.As(err, &nssErr) || nssErr.Code != code {
		t.Fatalf("err = %v, want an NSSError with code %s", err, code)
	}
}

func TestNSSLookup_ShouldServeEachStatusWhenAUserIsLookedUpByName(t *testing.T) {
	t.Parallel()

	disabledShell := "/usr/sbin/nologin"
	tests := []struct {
		name       string
		row        func(db *gorm.DB) model.PosixUser
		wantStatus string
		wantShell  *string
		wantMiss   bool
	}{
		{
			name:       "should serve an active account with its own shell when nothing disables it",
			row:        func(*gorm.DB) model.PosixUser { return model.PosixUser{Shell: ptr("/bin/bash")} },
			wantStatus: apitypes.NSSStatusActive, wantShell: ptr("/bin/bash"),
		},
		{
			name: "should serve a disabled account when an admin disabled the linked person",
			row: func(db *gorm.DB) model.PosixUser {
				id := seedNSSPerson(t, db, "sub-alice", "alice", nssAt(-time.Hour))
				db.Model(&model.User{}).Where("id = ?", id).Update("disabled_source", model.DisabledSourceAdmin)
				return model.PosixUser{UserID: &id, Shell: ptr("/bin/bash")}
			},
			wantStatus: apitypes.NSSStatusDisabled, wantShell: &disabledShell,
		},
		{
			name: "should serve a disabled account when SOC disabled the linked person",
			row: func(db *gorm.DB) model.PosixUser {
				id := seedNSSPerson(t, db, "sub-alice", "alice", nssAt(-time.Hour))
				db.Model(&model.User{}).Where("id = ?", id).Update("disabled_source", model.DisabledSourceSOC)
				return model.PosixUser{UserID: &id}
			},
			wantStatus: apitypes.NSSStatusDisabled, wantShell: &disabledShell,
		},
		{
			name: "should serve a disabled account when the directory sync disabled the linked person",
			row: func(db *gorm.DB) model.PosixUser {
				id := seedNSSPerson(t, db, "sub-alice", "alice", nssAt(-time.Hour))
				db.Model(&model.User{}).Where("id = ?", id).Update("disabled_source", model.DisabledSourceLDAPSync)
				return model.PosixUser{UserID: &id}
			},
			wantStatus: apitypes.NSSStatusDisabled, wantShell: &disabledShell,
		},
		{
			name:       "should serve a disabled account when the directory flags it disabled",
			row:        func(*gorm.DB) model.PosixUser { return model.PosixUser{DirDisabled: true, Shell: ptr("/bin/zsh")} },
			wantStatus: apitypes.NSSStatusDisabled, wantShell: &disabledShell,
		},
		{
			name:       "should serve a deleted account when it is tombstoned inside the retention",
			row:        func(*gorm.DB) model.PosixUser { return model.PosixUser{DeletedAt: nssAt(-24 * time.Hour)} },
			wantStatus: apitypes.NSSStatusDeleted, wantShell: &disabledShell,
		},
		{
			name: "should serve a deleted account when it is tombstoned exactly at the retention",
			row: func(*gorm.DB) model.PosixUser {
				return model.PosixUser{DeletedAt: nssAt(-2160 * time.Hour)}
			},
			wantStatus: apitypes.NSSStatusDeleted, wantShell: &disabledShell,
		},
		{
			name: "should not serve the account when its tombstone is past the retention",
			row: func(*gorm.DB) model.PosixUser {
				return model.PosixUser{DeletedAt: nssAt(-2160*time.Hour - time.Second)}
			},
			wantMiss: true,
		},
		{
			name: "should not serve the account when it is withheld by a collision",
			row: func(*gorm.DB) model.PosixUser {
				return model.PosixUser{WithheldAt: nssAt(-time.Hour), WithheldReason: "uid held twice"}
			},
			wantMiss: true,
		},
		{
			name: "should serve an active account with no shell when neither the entry nor the config has one",
			row:  func(*gorm.DB) model.PosixUser { return model.PosixUser{} },
			// The shipped default_shell is empty, so the host applies its own.
			wantStatus: apitypes.NSSStatusActive, wantShell: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			row := tt.row(db)
			row.Name, row.UID, row.GID, row.Gecos = "alice", 10001, 10001, "Alice Ashworth"
			seedPosixUser(t, db, row)

			got, err := newTestNSSLookup(nssLookupTestConfig(), db, nil).UserByName(t.Context(), "alice")
			if tt.wantMiss {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
				return
			}
			if err != nil {
				t.Fatalf("UserByName: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if !reflect.DeepEqual(got.Shell, tt.wantShell) {
				t.Errorf("shell = %v, want %v", deref(got.Shell), deref(tt.wantShell))
			}
			if got.Name != "alice" || got.UID != 10001 || got.Gecos != "Alice Ashworth" {
				t.Errorf("record = %+v, want alice 10001 with its gecos", got)
			}
		})
	}
}

// deref renders an optional string for a failure message.
func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestNSSLookup_ShouldServeTheHomeAndShellDefaultsWhenTheEntryHasNone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		home         *string
		shell        *string
		defaultHome  string
		defaultShell string
		wantHome     *string
		wantShell    *string
	}{
		{name: "should serve the entry's home when it has one", home: ptr("/srv/alice"), defaultHome: "/home/%u", wantHome: ptr("/srv/alice")},
		{name: "should expand nss.default_home when the entry has no home", defaultHome: "/home/%u", wantHome: ptr("/home/alice")},
		{name: "should expand every %u when the default names the user twice", defaultHome: "/home/%u/%u", wantHome: ptr("/home/alice/alice")},
		{name: "should serve no home when neither the entry nor the config has one", defaultHome: ""},
		{name: "should not serve a home that is not absolute when the entry holds one", home: ptr("home/alice"), defaultHome: "/home/%u"},
		{name: "should serve nss.default_shell when the entry has no shell", defaultHome: "/home/%u", defaultShell: "/bin/bash", wantHome: ptr("/home/alice"), wantShell: ptr("/bin/bash")},
		{name: "should prefer the entry's shell when both are set", shell: ptr("/bin/zsh"), defaultHome: "/home/%u", defaultShell: "/bin/bash", wantHome: ptr("/home/alice"), wantShell: ptr("/bin/zsh")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: 10001, Home: tt.home, Shell: tt.shell})
			c := nssLookupTestConfig()
			c.NSS.DefaultHome = tt.defaultHome
			c.NSS.DefaultShell = tt.defaultShell

			got, err := newTestNSSLookup(c, db, nil).UserByName(t.Context(), "alice")
			if err != nil {
				t.Fatalf("UserByName: %v", err)
			}
			if !reflect.DeepEqual(got.Home, tt.wantHome) {
				t.Errorf("home = %s, want %s", deref(got.Home), deref(tt.wantHome))
			}
			if !reflect.DeepEqual(got.Shell, tt.wantShell) {
				t.Errorf("shell = %s, want %s", deref(got.Shell), deref(tt.wantShell))
			}
		})
	}
}

func TestNSSLookup_ShouldServeTheShellPrefOnlyWhenTheAccountIsAPersons(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	personID := seedNSSPerson(t, db, "sub-alice", "alice", nil)
	seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: 10001, UserID: &personID, ShellPref: ptr("zsh")})
	seedPosixUser(t, db, model.PosixUser{Name: "svc-backup", UID: 20001, GID: 20001, ShellPref: ptr("fish")})
	lookups := newTestNSSLookup(nssLookupTestConfig(), db, nil)

	tests := []struct {
		name string
		user string
		want *string
	}{
		{name: "should serve the chosen shell when the account is linked to a person", user: "alice", want: ptr("zsh")},
		{name: "should serve no shell preference when a shared account has a stray one", user: "svc-backup", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lookups.UserByName(t.Context(), tt.user)
			if err != nil {
				t.Fatalf("UserByName: %v", err)
			}
			if !reflect.DeepEqual(got.ShellPref, tt.want) {
				t.Errorf("shell_pref = %s, want %s", deref(got.ShellPref), deref(tt.want))
			}
			if got.Status != apitypes.NSSStatusActive {
				t.Errorf("status = %q, want active for an account no person disabled", got.Status)
			}
		})
	}
}

func TestNSSLookup_ShouldMatchNamesByteForByteWhenTheCaseDiffers(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPosixUser(t, db, model.PosixUser{Name: "Ada.Lovelace", UID: 10001, GID: 10001})
	lookups := newTestNSSLookup(nssLookupTestConfig(), db, nil)

	tests := []struct {
		name     string
		lookup   string
		wantMiss bool
	}{
		{name: "should find the account when the name matches exactly, dot included", lookup: "Ada.Lovelace"},
		{name: "should miss when only the case differs", lookup: "ada.lovelace", wantMiss: true},
		{name: "should miss when the name is a prefix", lookup: "Ada", wantMiss: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lookups.UserByName(t.Context(), tt.lookup)
			if tt.wantMiss {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
				return
			}
			if err != nil {
				t.Errorf("UserByName(%q): %v", tt.lookup, err)
			}
		})
	}
}

func TestNSSLookup_ShouldRejectANameWhenItFailsThePOSIXRules(t *testing.T) {
	t.Parallel()

	lookups := newTestNSSLookup(nssLookupTestConfig(), testdb.New(t), nil)
	tests := []struct {
		name  string
		input string
	}{
		{name: "should reject the name when it is empty", input: ""},
		{name: "should reject the name when it is a dot", input: "."},
		{name: "should reject the name when it is two dots", input: ".."},
		{name: "should reject the name when it is all digits", input: "1234"},
		{name: "should reject the name when it reads as a negative number", input: "-1"},
		{name: "should reject the name when it holds a colon", input: "a:b"},
		{name: "should reject the name when it holds a slash", input: "a/b"},
		{name: "should reject the name when it starts with a space", input: " alice"},
		{name: "should reject the name when it ends with a space", input: "alice "},
		{name: "should reject the name when it holds a control byte", input: "ali\x01ce"},
		{name: "should reject the name when it is not valid UTF-8", input: "ali\xffce"},
		{name: "should reject the name when it is 33 bytes", input: strings.Repeat("a", 33)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, userErr := lookups.UserByName(t.Context(), tt.input)
			wantNSSCode(t, userErr, apitypes.ErrorCodeNSSBadRequest)
			_, groupErr := lookups.GroupByName(t.Context(), tt.input)
			wantNSSCode(t, groupErr, apitypes.ErrorCodeNSSBadRequest)
		})
	}
}

func TestNSSLookup_ShouldAcceptANameWhenItIsExactly32Bytes(t *testing.T) {
	t.Parallel()

	_, err := newTestNSSLookup(nssLookupTestConfig(), testdb.New(t), nil).UserByName(t.Context(), strings.Repeat("a", 32))
	wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
}

func TestParseNSSID_ShouldReadOnlyADecimalUint32WhenGivenAPathSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    uint32
		wantErr bool
	}{
		{name: "should read zero when the id is 0", raw: "0", want: 0},
		{name: "should read the id when it is ordinary", raw: "10001", want: 10001},
		{name: "should read the top of the range when the id is 4294967295", raw: "4294967295", want: 4294967295},
		{name: "should reject the id when it is one past the range", raw: "4294967296", wantErr: true},
		{name: "should reject the id when it is negative", raw: "-1", wantErr: true},
		{name: "should reject the id when it carries a plus sign", raw: "+5", wantErr: true},
		{name: "should reject the id when it is not a number", raw: "alice", wantErr: true},
		{name: "should reject the id when it is empty", raw: "", wantErr: true},
		{name: "should reject the id when it is hexadecimal", raw: "0x10", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseNSSID(tt.raw)
			if tt.wantErr {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSBadRequest)
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("ParseNSSID(%q) = %d, %v; want %d", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestNSSLookup_ShouldApplyAutoPrivateGroupsWhenTheUserIsRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       config.AutoPrivateGroupsMode
		uid, gid   uint32
		groups     []model.PosixGroup
		wantGID    uint32
		wantGroups []apitypes.NSSMembership
		wantMiss   bool
	}{
		{
			name: "should keep the directory gid and name its group when the mode is false",
			mode: config.AutoPrivateGroupsOff, uid: 10001, gid: 500,
			groups:  []model.PosixGroup{{Name: "staff", GID: 500}},
			wantGID: 500, wantGroups: []apitypes.NSSMembership{{GID: 500, Name: "staff"}},
		},
		{
			name: "should list no primary group when the mode is false and no group holds the gid",
			mode: config.AutoPrivateGroupsOff, uid: 10001, gid: 10001,
			wantGID: 10001, wantGroups: []apitypes.NSSMembership{},
		},
		{
			name: "should give every account a private group when the mode is true",
			mode: config.AutoPrivateGroupsOn, uid: 10001, gid: 500,
			groups:  []model.PosixGroup{{Name: "staff", GID: 500}},
			wantGID: 10001, wantGroups: []apitypes.NSSMembership{{GID: 10001, Name: "alice"}},
		},
		{
			name: "should not serve the account when the mode is true and a directory group holds its uid as gid",
			mode: config.AutoPrivateGroupsOn, uid: 10001, gid: 500,
			groups:   []model.PosixGroup{{Name: "engineering", GID: 10001}},
			wantMiss: true,
		},
		{
			name: "should not serve the account when the mode is true and a directory group has its name",
			mode: config.AutoPrivateGroupsOn, uid: 10001, gid: 500,
			groups:   []model.PosixGroup{{Name: "alice", GID: 30000}},
			wantMiss: true,
		},
		{
			name: "should not serve the account when the mode is true and a tombstoned group still holds the gid",
			mode: config.AutoPrivateGroupsOn, uid: 10001, gid: 500,
			groups:   []model.PosixGroup{{Name: "old", GID: 10001, DeletedAt: nssAt(-time.Hour)}},
			wantMiss: true,
		},
		{
			name: "should give a private group when the mode is hybrid, gid equals uid and no group holds it",
			mode: config.AutoPrivateGroupsHybrid, uid: 10001, gid: 10001,
			wantGID: 10001, wantGroups: []apitypes.NSSMembership{{GID: 10001, Name: "alice"}},
		},
		{
			name: "should name the directory group when the mode is hybrid and a real group holds the gid",
			mode: config.AutoPrivateGroupsHybrid, uid: 10001, gid: 10001,
			groups:  []model.PosixGroup{{Name: "alice-upg", GID: 10001}},
			wantGID: 10001, wantGroups: []apitypes.NSSMembership{{GID: 10001, Name: "alice-upg"}},
		},
		{
			name: "should keep the directory gid when the mode is hybrid and gid differs from uid",
			mode: config.AutoPrivateGroupsHybrid, uid: 10001, gid: 500,
			groups:  []model.PosixGroup{{Name: "staff", GID: 500}},
			wantGID: 500, wantGroups: []apitypes.NSSMembership{{GID: 500, Name: "staff"}},
		},
		{
			name: "should give no private group when the mode is hybrid and a directory group has the name",
			mode: config.AutoPrivateGroupsHybrid, uid: 10001, gid: 10001,
			groups:  []model.PosixGroup{{Name: "alice", GID: 30000}},
			wantGID: 10001, wantGroups: []apitypes.NSSMembership{},
		},
		{
			name: "should not list a tombstoned primary group when the mode is false",
			mode: config.AutoPrivateGroupsOff, uid: 10001, gid: 500,
			groups:  []model.PosixGroup{{Name: "staff", GID: 500, DeletedAt: nssAt(-time.Hour)}},
			wantGID: 500, wantGroups: []apitypes.NSSMembership{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: tt.uid, GID: tt.gid})
			for _, group := range tt.groups {
				seedPosixGroup(t, db, group)
			}
			c := nssLookupTestConfig()
			c.NSS.AutoPrivateGroups = tt.mode
			lookups := newTestNSSLookup(c, db, nil)

			byName, err := lookups.UserByName(t.Context(), "alice")
			byUID, uidErr := lookups.UserByUID(t.Context(), tt.uid)
			if tt.wantMiss {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
				wantNSSCode(t, uidErr, apitypes.ErrorCodeNSSNotFound)
				return
			}
			if err != nil || uidErr != nil {
				t.Fatalf("lookups: %v, %v", err, uidErr)
			}
			if byName.GID != tt.wantGID {
				t.Errorf("gid = %d, want %d", byName.GID, tt.wantGID)
			}
			if !reflect.DeepEqual(byName.Groups, tt.wantGroups) {
				t.Errorf("groups = %+v, want %+v", byName.Groups, tt.wantGroups)
			}
			if !reflect.DeepEqual(byName, byUID) {
				t.Errorf("by name %+v and by uid %+v differ", byName, byUID)
			}
		})
	}
}

func TestNSSLookup_ShouldListMembershipsPrimaryFirstThenByGIDWhenTheUserHasSeveral(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: 500})
	seedPosixGroup(t, db, model.PosixGroup{Name: "staff", GID: 500})
	seedPosixGroup(t, db, model.PosixGroup{Name: "wheel", GID: 10})
	seedPosixGroup(t, db, model.PosixGroup{Name: "sudo", GID: 27})
	seedPosixGroup(t, db, model.PosixGroup{Name: "retired", GID: 40, DeletedAt: nssAt(-time.Hour)})
	seedPosixGroup(t, db, model.PosixGroup{Name: "contested", GID: 41, WithheldAt: nssAt(-time.Hour)})
	seedPosixGroup(t, db, model.PosixGroup{Name: "other", GID: 42})
	// A membership row for the primary group too: listed once, first.
	seedPosixMembers(t, db, 500, 10001)
	seedPosixMembers(t, db, 27, 10001)
	seedPosixMembers(t, db, 10, 10001)
	seedPosixMembers(t, db, 40, 10001)
	seedPosixMembers(t, db, 41, 10001)
	seedPosixMembers(t, db, 42, 20001)

	got, err := newTestNSSLookup(nssLookupTestConfig(), db, nil).UserByName(t.Context(), "alice")
	if err != nil {
		t.Fatalf("UserByName: %v", err)
	}
	want := []apitypes.NSSMembership{{GID: 500, Name: "staff"}, {GID: 10, Name: "wheel"}, {GID: 27, Name: "sudo"}}
	if !reflect.DeepEqual(got.Groups, want) {
		t.Errorf("groups = %+v, want %+v", got.Groups, want)
	}
}

func TestNSSLookup_ShouldServeGroupMembersWhenAGroupIsLookedUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		group       model.PosixGroup
		maxMembers  int
		wantMembers []string
		wantTrunc   bool
		wantMiss    bool
	}{
		{
			name:        "should list the servable members by uid when the group is live",
			group:       model.PosixGroup{Name: "eng", GID: 700},
			maxMembers:  2000,
			wantMembers: []string{"carol", "alice", "bob", "dave"},
		},
		{
			name:        "should cap the members and flag it when the group is larger than the cap",
			group:       model.PosixGroup{Name: "eng", GID: 700},
			maxMembers:  2,
			wantMembers: []string{"carol", "alice"},
			wantTrunc:   true,
		},
		{
			name:        "should not truncate when the group is exactly at the cap",
			group:       model.PosixGroup{Name: "eng", GID: 700},
			maxMembers:  4,
			wantMembers: []string{"carol", "alice", "bob", "dave"},
		},
		{
			name:        "should serve no members when the group is tombstoned inside the retention",
			group:       model.PosixGroup{Name: "eng", GID: 700, DeletedAt: nssAt(-time.Hour)},
			maxMembers:  2000,
			wantMembers: []string{},
		},
		{
			name:       "should not serve the group when its tombstone is past the retention",
			group:      model.PosixGroup{Name: "eng", GID: 700, DeletedAt: nssAt(-2161 * time.Hour)},
			maxMembers: 2000, wantMiss: true,
		},
		{
			name:       "should not serve the group when it is withheld",
			group:      model.PosixGroup{Name: "eng", GID: 700, WithheldAt: nssAt(-time.Hour)},
			maxMembers: 2000, wantMiss: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			personID := seedNSSPerson(t, db, "sub-bob", "bob", nssAt(-time.Hour))
			seedPosixUser(t, db, model.PosixUser{Name: "carol", UID: 9000, GID: 700})
			seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: 700})
			// Disabled people keep their memberships.
			seedPosixUser(t, db, model.PosixUser{Name: "bob", UID: 10002, GID: 700, UserID: &personID})
			// So do tombstones inside the retention.
			seedPosixUser(t, db, model.PosixUser{Name: "dave", UID: 10003, GID: 700, DeletedAt: nssAt(-time.Hour)})
			seedPosixUser(t, db, model.PosixUser{Name: "erin", UID: 10004, GID: 700, WithheldAt: nssAt(-time.Hour)})
			seedPosixUser(t, db, model.PosixUser{Name: "frank", UID: 10005, GID: 700, DeletedAt: nssAt(-2161 * time.Hour)})
			seedPosixGroup(t, db, tt.group)
			seedPosixMembers(t, db, 700, 9000, 10001, 10002, 10003, 10004, 10005)
			c := nssLookupTestConfig()
			c.NSS.MaxGroupMembers = tt.maxMembers
			lookups := newTestNSSLookup(c, db, nil)

			byName, err := lookups.GroupByName(t.Context(), "eng")
			byGID, gidErr := lookups.GroupByGID(t.Context(), 700)
			if tt.wantMiss {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
				wantNSSCode(t, gidErr, apitypes.ErrorCodeNSSNotFound)
				return
			}
			if err != nil || gidErr != nil {
				t.Fatalf("lookups: %v, %v", err, gidErr)
			}
			want := &apitypes.NSSGroupResponse{Name: "eng", GID: 700, Members: tt.wantMembers, MembersTruncated: tt.wantTrunc}
			if !reflect.DeepEqual(byName, want) {
				t.Errorf("by name = %+v, want %+v", byName, want)
			}
			if !reflect.DeepEqual(byGID, want) {
				t.Errorf("by gid = %+v, want %+v", byGID, want)
			}
		})
	}
}

func TestNSSLookup_ShouldResolvePrivateGroupsWhenAGroupIsLookedUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mode     config.AutoPrivateGroupsMode
		user     model.PosixUser
		groups   []model.PosixGroup
		wantMiss bool
	}{
		{name: "should resolve the private group when the mode is true", mode: config.AutoPrivateGroupsOn,
			user: model.PosixUser{Name: "alice", UID: 10001, GID: 500}},
		{name: "should resolve the private group when the mode is hybrid and gid equals uid", mode: config.AutoPrivateGroupsHybrid,
			user: model.PosixUser{Name: "alice", UID: 10001, GID: 10001}},
		{name: "should not resolve a private group when the mode is false", mode: config.AutoPrivateGroupsOff,
			user: model.PosixUser{Name: "alice", UID: 10001, GID: 10001}, wantMiss: true},
		{name: "should not resolve a private group when the mode is hybrid and gid differs from uid", mode: config.AutoPrivateGroupsHybrid,
			user: model.PosixUser{Name: "alice", UID: 10001, GID: 500}, wantMiss: true},
		{name: "should not resolve a private group when its account is withheld", mode: config.AutoPrivateGroupsOn,
			user: model.PosixUser{Name: "alice", UID: 10001, GID: 500, WithheldAt: nssAt(-time.Hour)}, wantMiss: true},
		{name: "should not resolve a private group when the mode is true and a directory group has the name", mode: config.AutoPrivateGroupsOn,
			user: model.PosixUser{Name: "alice", UID: 10001, GID: 500}, groups: []model.PosixGroup{{Name: "alice", GID: 30000, WithheldAt: nssAt(-time.Hour)}}, wantMiss: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedPosixUser(t, db, tt.user)
			for _, group := range tt.groups {
				seedPosixGroup(t, db, group)
			}
			c := nssLookupTestConfig()
			c.NSS.AutoPrivateGroups = tt.mode
			lookups := newTestNSSLookup(c, db, nil)

			byName, err := lookups.GroupByName(t.Context(), "alice")
			byGID, gidErr := lookups.GroupByGID(t.Context(), 10001)
			if tt.wantMiss {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
				wantNSSCode(t, gidErr, apitypes.ErrorCodeNSSNotFound)
				return
			}
			want := &apitypes.NSSGroupResponse{Name: "alice", GID: 10001, Members: []string{}}
			if err != nil || !reflect.DeepEqual(byName, want) {
				t.Errorf("by name = %+v, %v; want %+v", byName, err, want)
			}
			if gidErr != nil || !reflect.DeepEqual(byGID, want) {
				t.Errorf("by gid = %+v, %v; want %+v", byGID, gidErr, want)
			}
		})
	}
}

func TestNSSLookup_ShouldServeAUserAndAGroupWhenTheyShareAName(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPosixUser(t, db, model.PosixUser{Name: "ops", UID: 10001, GID: 500})
	seedPosixGroup(t, db, model.PosixGroup{Name: "ops", GID: 600})
	lookups := newTestNSSLookup(nssLookupTestConfig(), db, nil)

	user, err := lookups.UserByName(t.Context(), "ops")
	if err != nil || user.UID != 10001 {
		t.Errorf("UserByName(ops) = %+v, %v; want uid 10001", user, err)
	}
	group, err := lookups.GroupByName(t.Context(), "ops")
	if err != nil || group.GID != 600 {
		t.Errorf("GroupByName(ops) = %+v, %v; want gid 600", group, err)
	}
}

func TestNSSLookup_ShouldAskTheDirectoryOnlyWhenTheDatabaseHasNoServableRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		seed      func(t *testing.T, db *gorm.DB)
		refresher func(t *testing.T, db *gorm.DB) *fakeNSSRefresher
		lookup    func(ctx context.Context, s *NSSLookupService) error
		wantCalls []string
		wantErr   func(t *testing.T, err error)
	}{
		{
			name: "should not ask the directory when the row is in the database",
			seed: func(t *testing.T, db *gorm.DB) {
				seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: 10001})
			},
			refresher: func(*testing.T, *gorm.DB) *fakeNSSRefresher { return &fakeNSSRefresher{} },
			lookup:    func(ctx context.Context, s *NSSLookupService) error { _, err := s.UserByName(ctx, "alice"); return err },
			wantCalls: nil,
		},
		{
			name: "should serve the row the refresh wrote when the name was unknown",
			refresher: func(t *testing.T, db *gorm.DB) *fakeNSSRefresher {
				return &fakeNSSRefresher{found: true, onRefresh: func(string) {
					seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: 10001})
				}}
			},
			lookup:    func(ctx context.Context, s *NSSLookupService) error { _, err := s.UserByName(ctx, "alice"); return err },
			wantCalls: []string{"user-name:alice"},
		},
		{
			name:      "should answer not found when the directory has no such uid",
			refresher: func(*testing.T, *gorm.DB) *fakeNSSRefresher { return &fakeNSSRefresher{} },
			lookup:    func(ctx context.Context, s *NSSLookupService) error { _, err := s.UserByUID(ctx, 4242); return err },
			wantCalls: []string{"user-uid:4242"},
			wantErr: func(t *testing.T, err error) {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
			},
		},
		{
			name: "should ask the directory when the only row is withheld",
			seed: func(t *testing.T, db *gorm.DB) {
				seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: 10001, WithheldAt: nssAt(-time.Hour)})
			},
			refresher: func(*testing.T, *gorm.DB) *fakeNSSRefresher { return &fakeNSSRefresher{} },
			lookup:    func(ctx context.Context, s *NSSLookupService) error { _, err := s.UserByName(ctx, "alice"); return err },
			wantCalls: []string{"user-name:alice"},
			wantErr: func(t *testing.T, err error) {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
			},
		},
		{
			name: "should answer unavailable rather than not found when the directory cannot be asked",
			refresher: func(*testing.T, *gorm.DB) *fakeNSSRefresher {
				return &fakeNSSRefresher{err: errors.New("ldap: connection refused")}
			},
			lookup:    func(ctx context.Context, s *NSSLookupService) error { _, err := s.UserByName(ctx, "alice"); return err },
			wantCalls: []string{"user-name:alice"},
			wantErr: func(t *testing.T, err error) {
				var unavailable *errorresponses.UserStatusCheckError
				if !errors.As(err, &unavailable) || unavailable.HTTPStatusCode() != 503 {
					t.Errorf("err = %v, want the 503 unavailable error", err)
				}
			},
		},
		{
			name: "should serve a row that appeared meanwhile when the directory cannot be asked",
			refresher: func(t *testing.T, db *gorm.DB) *fakeNSSRefresher {
				return &fakeNSSRefresher{err: errors.New("ldap: timeout"), onRefresh: func(string) {
					seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: 10001})
				}}
			},
			lookup:    func(ctx context.Context, s *NSSLookupService) error { _, err := s.UserByName(ctx, "alice"); return err },
			wantCalls: []string{"user-name:alice"},
		},
		{
			name:      "should ask for the group and then its would-be private group's account when a group is unknown",
			refresher: func(*testing.T, *gorm.DB) *fakeNSSRefresher { return &fakeNSSRefresher{} },
			lookup:    func(ctx context.Context, s *NSSLookupService) error { _, err := s.GroupByName(ctx, "eng"); return err },
			wantCalls: []string{"group-name:eng", "user-name:eng"},
			wantErr: func(t *testing.T, err error) {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
			},
		},
		{
			name: "should stop at the group refresh when it wrote the group",
			refresher: func(t *testing.T, db *gorm.DB) *fakeNSSRefresher {
				return &fakeNSSRefresher{found: true, onRefresh: func(string) {
					seedPosixGroup(t, db, model.PosixGroup{Name: "eng", GID: 700})
				}}
			},
			lookup:    func(ctx context.Context, s *NSSLookupService) error { _, err := s.GroupByGID(ctx, 700); return err },
			wantCalls: []string{"group-gid:700"},
		},
		{
			name:      "should answer unavailable when neither group nor account refresh could reach the directory",
			refresher: func(*testing.T, *gorm.DB) *fakeNSSRefresher { return &fakeNSSRefresher{err: errors.New("ldap down")} },
			lookup:    func(ctx context.Context, s *NSSLookupService) error { _, err := s.GroupByGID(ctx, 700); return err },
			wantCalls: []string{"group-gid:700", "user-uid:700"},
			wantErr: func(t *testing.T, err error) {
				var unavailable *errorresponses.UserStatusCheckError
				if !errors.As(err, &unavailable) {
					t.Errorf("err = %v, want the 503 unavailable error", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			if tt.seed != nil {
				tt.seed(t, db)
			}
			refresher := tt.refresher(t, db)
			c := nssLookupTestConfig()
			c.NSS.AutoPrivateGroups = config.AutoPrivateGroupsHybrid
			err := tt.lookup(t.Context(), newTestNSSLookup(c, db, refresher))
			if tt.wantErr != nil {
				tt.wantErr(t, err)
			} else if err != nil {
				t.Errorf("lookup: %v", err)
			}
			if got := refresher.recorded(); !reflect.DeepEqual(got, tt.wantCalls) {
				t.Errorf("refresher calls = %v, want %v", got, tt.wantCalls)
			}
		})
	}
}

func TestNSSLookup_ShouldAskOnlyForTheGroupWhenPrivateGroupsAreOff(t *testing.T) {
	t.Parallel()

	refresher := &fakeNSSRefresher{}
	_, err := newTestNSSLookup(nssLookupTestConfig(), testdb.New(t), refresher).GroupByName(t.Context(), "eng")
	wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
	if got := refresher.recorded(); !reflect.DeepEqual(got, []string{"group-name:eng"}) {
		t.Errorf("refresher calls = %v, want only the group refresh", got)
	}
}

func TestNSSLookup_ShouldAnswerNotFoundWhenNoRefresherIsWired(t *testing.T) {
	t.Parallel()

	lookups := newTestNSSLookup(nssLookupTestConfig(), testdb.New(t), nil)
	_, userErr := lookups.UserByName(t.Context(), "alice")
	wantNSSCode(t, userErr, apitypes.ErrorCodeNSSNotFound)
	_, groupErr := lookups.GroupByGID(t.Context(), 1)
	wantNSSCode(t, groupErr, apitypes.ErrorCodeNSSNotFound)
}

func TestNSSLookup_ShouldReturnTheDatabaseErrorWhenTheReadFails(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	lookups := newTestNSSLookup(nssLookupTestConfig(), db, nil)

	var nssErr *errorresponses.NSSError
	if _, err := lookups.UserByName(t.Context(), "alice"); err == nil || errors.As(err, &nssErr) {
		t.Errorf("UserByName on a closed database = %v, want a plain database error", err)
	}
	if _, err := lookups.GroupByName(t.Context(), "eng"); err == nil || errors.As(err, &nssErr) {
		t.Errorf("GroupByName on a closed database = %v, want a plain database error", err)
	}
}
