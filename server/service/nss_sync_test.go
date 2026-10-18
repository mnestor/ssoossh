package service

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/server/model"
)

func TestNSSFullSync_ShouldStoreEveryEntryWhenTheDirectoryIsEnumerated(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada.lovelace", 1001, 1001, map[string][]string{"displayName": {"Ada Lovelace"}}))
	// A service account: never logs in, no home in the directory.
	h.dir.put(nssAccountEntry("uid=svc-backup,ou=services,dc=test", "svc-backup", 1002, 1002, map[string][]string{"homeDirectory": nil}))
	h.dir.put(nssGroupEntry("devs", 5000, "ada.lovelace", "svc-backup", "not-a-posix-account"))

	result := h.fullSync(t)

	if !result.Full || result.Accounts.Created != 2 || result.Groups.Created != 1 {
		t.Errorf("result = %+v, want a full run creating 2 accounts and 1 group", result)
	}
	if got := h.dir.paged; got != 2 {
		t.Errorf("paged searches = %d, want 2 (accounts and groups)", got)
	}
	ada := h.accountByName(t, "ada.lovelace")
	if ada.UID != 1001 || ada.Gecos != "Ada Lovelace" || ada.Home == nil || *ada.Home != "/home/ada.lovelace" {
		t.Errorf("ada = %+v, want uid 1001, gecos and home from the directory", ada)
	}
	if svc := h.accountByName(t, "svc-backup"); svc.Home != nil || svc.UserID != nil {
		t.Errorf("svc-backup = %+v, want no home and no person", svc)
	}
	if got := h.members(t, 5000); !reflect.DeepEqual(got, []uint32{1001, 1002}) {
		t.Errorf("members of devs = %v, want the two stored accounts only", got)
	}
}

func TestNSSFullSync_ShouldMarkAnAccountDisabledWhenTheDisabledFilterMatchesIt(t *testing.T) {
	t.Parallel()

	cfg := nssTestConfig()
	cfg.NSS.DisabledFilter = "(nsAccountLock=TRUE)"
	h := newNSSHarness(t, cfg)
	h.dir.put(nssAccountEntry("", "locked", 1, 1, map[string][]string{"nsAccountLock": {"TRUE"}}))
	h.dir.put(nssAccountEntry("", "open", 2, 2, nil))

	h.fullSync(t)

	tests := []struct {
		name string
		want bool
	}{{"locked", true}, {"open", false}}
	for _, tt := range tests {
		if got := h.accountByName(t, tt.name).DirDisabled; got != tt.want {
			t.Errorf("%s dir_disabled = %v, want %v", tt.name, got, tt.want)
		}
	}
	if h.dir.paged != 3 {
		t.Errorf("paged searches = %d, want 3 (accounts, disabled accounts, groups)", h.dir.paged)
	}
}

func TestNSSFullSync_ShouldSkipAnEntryWhenAValueDoesNotParse(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "broken", 1, 1, map[string][]string{"uidNumber": {"one"}}))
	h.dir.put(nssAccountEntry("", "fine", 2, 2, nil))

	result := h.fullSync(t)

	if result.Accounts.Skipped != 1 || result.Accounts.Created != 1 {
		t.Errorf("accounts = %+v, want one skipped and one created", result.Accounts)
	}
	if n := h.count(t, &model.PosixUser{}, "name = ?", "broken"); n != 0 {
		t.Errorf("broken rows = %d, want none: no partial rows", n)
	}
}

func TestNSSFullSync_ShouldResolveMembersWhenTheMemberAttributeHoldsDNs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		attr    string
		members []string
	}{
		{name: "should match member DNs when case and spacing differ", attr: "member", members: []string{"UID=Ada,OU=People,DC=Test", "uid=bob, ou=people, dc=test", "uid=ghost,ou=people,dc=test"}},
		{name: "should match uniqueMember DNs when they carry a unique identifier", attr: "uniqueMember", members: []string{"uid=ada,ou=people,dc=test#'0101'B", "uid=bob,ou=people,dc=test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssTestConfig()
			cfg.NSS.Attributes.GroupMember = tt.attr
			h := newNSSHarness(t, cfg)
			h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
			h.dir.put(nssAccountEntry("", "bob", 2, 2, nil))
			h.dir.put(nssEntry("cn=ops,ou=groups,dc=test", map[string][]string{
				"objectClass": {"posixGroup"}, "cn": {"ops"}, "gidNumber": {"700"}, tt.attr: tt.members,
			}))

			h.fullSync(t)

			if got := h.members(t, 700); !reflect.DeepEqual(got, []uint32{1, 2}) {
				t.Errorf("members = %v, want [1 2]", got)
			}
		})
	}
}

func TestNSSFullSync_ShouldReplaceAGroupsMembersWhenItIsReadAgain(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
	h.dir.put(nssAccountEntry("", "bob", 2, 2, nil))
	h.dir.put(nssGroupEntry("ops", 700, "ada", "bob"))
	h.fullSync(t)

	h.dir.put(nssGroupEntry("ops", 700, "bob"))
	h.clock.advance(time.Hour)
	h.fullSync(t)

	if got := h.members(t, 700); !reflect.DeepEqual(got, []uint32{2}) {
		t.Errorf("members = %v, want only bob after ada was dropped", got)
	}
}

func TestNSSFullSync_ShouldHoldAChangedNumberPendingWhenTheDirectoryChangesIt(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 100, nil))
	h.dir.put(nssGroupEntry("staff", 100))
	h.fullSync(t)

	steps := []struct {
		name        string
		uid, gid    uint32
		groupGID    uint32
		advance     time.Duration
		wantPending bool
		wantAlerts  int64
	}{
		{name: "should hold the uid and gid pending when both change", uid: 2001, gid: 200, groupGID: 300, wantPending: true, wantAlerts: 2},
		{name: "should not alert again when the same day sees it again", uid: 2001, gid: 200, groupGID: 300, advance: time.Hour, wantPending: true, wantAlerts: 2},
		{name: "should alert again when a day has passed", uid: 2001, gid: 200, groupGID: 300, advance: 24 * time.Hour, wantPending: true, wantAlerts: 4},
		{name: "should clear the pending values when the directory reverts", uid: 1001, gid: 100, groupGID: 100, advance: time.Hour, wantPending: false, wantAlerts: 4},
	}
	for _, step := range steps {
		h.clock.advance(step.advance)
		h.dir.put(nssAccountEntry("", "ada", step.uid, step.gid, nil))
		h.dir.put(nssGroupEntry("staff", step.groupGID))
		h.fullSync(t)

		ada := h.accountByName(t, "ada")
		staff := h.groupByName(t, "staff")
		if ada.UID != 1001 || ada.GID != 100 || staff.GID != 100 {
			t.Errorf("%s: stored numbers moved: ada %d/%d, staff %d", step.name, ada.UID, ada.GID, staff.GID)
		}
		pending := ada.PendingUID != nil && ada.PendingGID != nil && staff.PendingGID != nil && ada.PendingSince != nil
		cleared := ada.PendingUID == nil && ada.PendingGID == nil && staff.PendingGID == nil && ada.PendingSince == nil
		if (step.wantPending && !pending) || (!step.wantPending && !cleared) {
			t.Errorf("%s: ada pending %v/%v since %v, staff pending %v", step.name, ada.PendingUID, ada.PendingGID, ada.PendingSince, staff.PendingGID)
		}
		if got := h.audits(t, AuditNSSIDChangePending); got != step.wantAlerts {
			t.Errorf("%s: nss.id_change_pending events = %d, want %d", step.name, got, step.wantAlerts)
		}
	}
}

func TestNSSFullSync_ShouldKeepThePendingClockWhenTheSameValueIsSeenAgain(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 100, nil))
	h.fullSync(t)
	h.dir.put(nssAccountEntry("", "ada", 2001, 100, nil))
	h.fullSync(t)
	first := *h.accountByName(t, "ada").PendingSince

	h.clock.advance(time.Hour)
	h.fullSync(t)
	if got := *h.accountByName(t, "ada").PendingSince; !got.Equal(first) {
		t.Errorf("pending_since = %v, want it kept at %v", got, first)
	}

	h.clock.advance(time.Hour)
	h.dir.put(nssAccountEntry("", "ada", 3001, 100, nil))
	h.fullSync(t)
	if got := h.accountByName(t, "ada"); got.PendingSince.Equal(first) || *got.PendingUID != 3001 {
		t.Errorf("pending = %v since %v, want the new value with a new clock", *got.PendingUID, got.PendingSince)
	}
}

func TestNSSFullSync_ShouldKeepTheUidWhenARenamedEntryIsMatchedByItsID(t *testing.T) {
	t.Parallel()

	cfg := nssTestConfig()
	cfg.LDAP.IDAttribute = "entryUUID"
	h := newNSSHarness(t, cfg)
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, map[string][]string{"entryUUID": {"id-ada"}}))
	h.fullSync(t)
	before := h.accountByName(t, "ada")

	h.dir.remove("uid=ada,ou=people,dc=test")
	h.dir.put(nssAccountEntry("uid=ada.byron,ou=staff,dc=test", "ada.byron", 1001, 1001, map[string][]string{"entryUUID": {"id-ada"}}))
	h.clock.advance(time.Hour)
	h.fullSync(t)

	after := h.accountByName(t, "ada.byron")
	if after.ID != before.ID || after.UID != 1001 || after.DN != "uid=ada.byron,ou=staff,dc=test" || after.MissingSince != nil {
		t.Errorf("after the rename = %+v, want the same row, uid and a new DN", after)
	}
}

func TestNSSFullSync_ShouldWithholdBothWhenAnEntryMovesWithoutAnIDAttribute(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
	h.fullSync(t)

	h.dir.remove("uid=ada,ou=people,dc=test")
	h.dir.put(nssAccountEntry("uid=ada,ou=staff,dc=test", "ada", 1001, 1001, nil))
	h.clock.advance(time.Hour)
	result := h.fullSync(t)

	if result.Accounts.Refused != 1 {
		t.Errorf("refused = %d, want the moved entry refused", result.Accounts.Refused)
	}
	old := h.accountByName(t, "ada")
	if old.DN != "uid=ada,ou=people,dc=test" || old.WithheldAt == nil || old.MissingSince == nil {
		t.Errorf("old row = %+v, want it missing and withheld", old)
	}
	if n := h.count(t, &model.NSSCollision{}, "holder_id = ?", old.ID); n != 2 {
		t.Errorf("collisions = %d, want uid and name", n)
	}
	if h.audits(t, AuditNSSUIDCollision) != 1 || h.audits(t, AuditNSSNameCollision) != 1 {
		t.Errorf("alerts = %d uid, %d name; want one each", h.audits(t, AuditNSSUIDCollision), h.audits(t, AuditNSSNameCollision))
	}
}

func TestNSSFullSync_ShouldWithholdTheHolderWhenTwoEntriesShareAUid(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 3000, 3000, nil))
	h.dir.put(nssAccountEntry("", "eve", 3000, 3000, nil))
	h.fullSync(t)

	if n := h.count(t, &model.PosixUser{}, ""); n != 1 {
		t.Fatalf("stored accounts = %d, want only the holder", n)
	}
	var holder model.PosixUser
	h.db.First(&holder)
	if holder.WithheldAt == nil || !strings.Contains(holder.WithheldReason, "uid 3000 is also carried by") {
		t.Errorf("holder = %+v, want it withheld with a reason naming the uid", holder)
	}

	h.clock.advance(time.Hour)
	h.fullSync(t)
	if got := h.audits(t, AuditNSSUIDCollision); got != 1 {
		t.Errorf("nss.uid_collision events = %d, want one per day", got)
	}

	h.dir.remove("uid=eve,ou=people,dc=test")
	h.clock.advance(time.Hour)
	h.fullSync(t)
	var after model.PosixUser
	h.db.First(&after, "id = ?", holder.ID)
	if after.WithheldAt != nil || after.WithheldReason != "" {
		t.Errorf("holder = %+v, want it served once the other entry has gone", after)
	}
	if n := h.count(t, &model.NSSCollision{}, ""); n != 0 {
		t.Errorf("collisions = %d, want none left", n)
	}
}

func TestNSSFullSync_ShouldWithholdBothWhenTwoLiveEntriesShareAName(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("uid=alice,ou=people,dc=test", "alice", 1, 1, nil))
	h.dir.put(nssAccountEntry("uid=alice,ou=contractors,dc=test", "alice", 2, 2, nil))

	steps := []struct {
		name       string
		advance    time.Duration
		wantAlerts int64
	}{
		{name: "should alert when the collision is first seen", wantAlerts: 1},
		{name: "should not alert again on the same day", advance: time.Hour, wantAlerts: 1},
		{name: "should alert again on the next day", advance: 24 * time.Hour, wantAlerts: 2},
	}
	for _, step := range steps {
		h.clock.advance(step.advance)
		h.fullSync(t)
		alice := h.accountByName(t, "alice")
		if alice.WithheldAt == nil {
			t.Errorf("%s: alice not withheld", step.name)
		}
		if n := h.count(t, &model.PosixUser{}, ""); n != 1 {
			t.Errorf("%s: stored accounts = %d, want the second alice refused", step.name, n)
		}
		if got := h.audits(t, AuditNSSNameCollision); got != step.wantAlerts {
			t.Errorf("%s: nss.name_collision events = %d, want %d", step.name, got, step.wantAlerts)
		}
	}
}

func TestNSSFullSync_ShouldWithholdBothWhenARenameTargetsAHeldName(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "alice", 1, 1, nil))
	h.dir.put(nssAccountEntry("", "bob", 2, 2, nil))
	h.fullSync(t)

	h.dir.put(nssAccountEntry("uid=bob,ou=people,dc=test", "alice", 2, 2, nil))
	h.clock.advance(time.Hour)
	h.fullSync(t)

	alice, bob := h.accountByName(t, "alice"), h.accountByName(t, "bob")
	if alice.WithheldAt == nil || bob.WithheldAt == nil {
		t.Errorf("alice withheld %v, bob withheld %v; want both", alice.WithheldAt, bob.WithheldAt)
	}
	if bob.DN != "uid=bob,ou=people,dc=test" {
		t.Errorf("bob = %+v, want the row kept under its old name", bob)
	}
}

func TestNSSFullSync_ShouldWithholdANewEntryWhenATombstoneHoldsItsName(t *testing.T) {
	t.Parallel()

	cfg := nssTestConfig()
	cfg.NSS.TombstoneAfter = 0
	h := newNSSHarness(t, cfg)
	h.dir.put(nssAccountEntry("", "alice", 1, 1, nil))
	h.fullSync(t)
	h.dir.remove("uid=alice,ou=people,dc=test")
	h.clock.advance(time.Hour)
	h.fullSync(t)
	if old := h.accountByName(t, "alice"); old.DeletedAt == nil {
		t.Fatalf("alice = %+v, want a tombstone", old)
	}

	h.dir.put(nssAccountEntry("uid=alice,ou=new,dc=test", "alice", 50, 50, nil))
	h.clock.advance(time.Hour)
	result := h.fullSync(t)
	if result.Accounts.Refused != 1 {
		t.Errorf("refused = %d, want the new alice refused while the tombstone holds the name", result.Accounts.Refused)
	}

	if err := h.svc.PurgeUserTombstone(t.Context(), 1, nil); err != nil {
		t.Fatalf("PurgeUserTombstone() error = %v", err)
	}
	h.clock.advance(time.Hour)
	h.fullSync(t)
	if got := h.accountByName(t, "alice"); got.UID != 50 || got.WithheldAt != nil {
		t.Errorf("alice = %+v, want the new entry stored and served after the purge", got)
	}
}

func TestNSSFullSync_ShouldStoreBothWhenAnAccountAndAGroupShareAName(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
	h.dir.put(nssGroupEntry("ada", 1001, "ada"))
	h.fullSync(t)

	if a, g := h.accountByName(t, "ada"), h.groupByName(t, "ada"); a.WithheldAt != nil || g.WithheldAt != nil {
		t.Errorf("account withheld %v, group withheld %v; want neither", a.WithheldAt, g.WithheldAt)
	}
}

func TestNSSFullSync_ShouldWithholdTheHolderWhenTwoGroupsShareAGid(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssGroupEntry("ops", 700))
	h.dir.put(nssGroupEntry("ops2", 700))
	result := h.fullSync(t)

	if result.Groups.Refused != 1 || h.groupByName(t, "ops").WithheldAt == nil {
		t.Errorf("groups = %+v, want ops withheld and ops2 refused", result.Groups)
	}
	if got := h.audits(t, AuditNSSGIDCollision); got != 1 {
		t.Errorf("nss.gid_collision events = %d, want 1", got)
	}
}

func TestNSSFullSync_ShouldTombstoneAnEntryWhenItStaysMissing(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
	h.dir.put(nssGroupEntry("ops", 700, "ada"))
	h.fullSync(t)
	h.dir.remove("uid=ada,ou=people,dc=test")
	h.dir.remove("cn=ops,ou=groups,dc=test")

	steps := []struct {
		name          string
		advance       time.Duration
		restore       bool
		wantMissing   bool
		wantTombstone bool
		wantMembers   int
	}{
		{name: "should mark the entries missing when a full sync does not see them", advance: time.Hour, wantMissing: true, wantMembers: 1},
		{name: "should keep them missing when tombstone_after has not passed", advance: 100 * time.Hour, wantMissing: true, wantMembers: 1},
		{name: "should tombstone them when tombstone_after has passed", advance: 68 * time.Hour, wantMissing: true, wantTombstone: true, wantMembers: 0},
		{name: "should clear both when the entries reappear", advance: time.Hour, restore: true, wantMembers: 1},
	}
	for _, step := range steps {
		h.clock.advance(step.advance)
		if step.restore {
			h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
			h.dir.put(nssGroupEntry("ops", 700, "ada"))
		}
		h.fullSync(t)

		ada, ops := h.accountByName(t, "ada"), h.groupByName(t, "ops")
		for _, got := range []struct {
			what             string
			missing, deleted *time.Time
		}{{"ada", ada.MissingSince, ada.DeletedAt}, {"ops", ops.MissingSince, ops.DeletedAt}} {
			if (got.missing != nil) != step.wantMissing || (got.deleted != nil) != step.wantTombstone {
				t.Errorf("%s: %s missing %v deleted %v", step.name, got.what, got.missing, got.deleted)
			}
		}
		if n := len(h.members(t, 700)); n != step.wantMembers {
			t.Errorf("%s: members = %d, want %d", step.name, n, step.wantMembers)
		}
	}
}

func TestNSSSync_ShouldCountNothingMissingWhenTheDirectoryFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		breakDir func(*nssFakeDirectory)
	}{
		{name: "should leave every row when the directory cannot be reached", breakDir: func(f *nssFakeDirectory) { f.dialErr = errors.New("connection refused") }},
		{name: "should leave every row when the group search fails", breakDir: func(f *nssFakeDirectory) {
			f.fail = func(req *ldap.SearchRequest) error {
				if strings.Contains(req.Filter, "posixGroup") {
					return ldap.NewError(ldap.LDAPResultBusy, errors.New("busy"))
				}
				return nil
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSHarness(t, nil)
			h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
			h.dir.put(nssGroupEntry("ops", 700, "ada"))
			h.fullSync(t)
			h.dir.remove("uid=ada,ou=people,dc=test")
			h.dir.remove("cn=ops,ou=groups,dc=test")
			tt.breakDir(h.dir)

			h.clock.advance(time.Hour)
			if _, err := h.svc.FullSync(t.Context()); err == nil {
				t.Fatal("FullSync() error = nil, want the failure reported")
			}
			if g := h.groupByName(t, "ops"); g.MissingSince != nil {
				t.Errorf("ops missing_since = %v, want nothing counted as missing", g.MissingSince)
			}
		})
	}
}

func TestNSSIncrementalSync_ShouldRunAsFullWhenNoEarlierRunSucceeded(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
	result, err := h.svc.IncrementalSync(t.Context())
	if err != nil {
		t.Fatalf("IncrementalSync() error = %v", err)
	}
	if !result.Full || result.Accounts.Created != 1 {
		t.Errorf("result = %+v, want a full run", result)
	}
}

func TestNSSIncrementalSync_ShouldReadOnlyModifiedEntriesWhenABaselineExists(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1, 1, map[string][]string{"modifyTimestamp": {"20261017110000Z"}}))
	h.dir.put(nssAccountEntry("", "bob", 2, 2, map[string][]string{"modifyTimestamp": {"20261017110000Z"}}))
	h.fullSync(t)

	h.clock.advance(15 * time.Minute)
	h.dir.put(nssAccountEntry("", "ada", 1, 1, map[string][]string{"modifyTimestamp": {"20261017120500Z"}, "displayName": {"Ada L"}}))
	h.dir.remove("uid=bob,ou=people,dc=test")
	result, err := h.svc.IncrementalSync(t.Context())
	if err != nil {
		t.Fatalf("IncrementalSync() error = %v", err)
	}

	if result.Full || result.Accounts.Seen != 1 || result.Accounts.Updated != 1 {
		t.Errorf("result = %+v, want an incremental run reading only ada", result)
	}
	// Five minutes before the full run began at 12:00.
	want := "(&(objectClass=posixAccount)(modifyTimestamp>=20261017115500Z))"
	if !slices.Contains(h.dir.searched(), want) {
		t.Errorf("searches = %v, want %s", h.dir.searched(), want)
	}
	if got := h.accountByName(t, "ada").Gecos; got != "Ada L" {
		t.Errorf("ada gecos = %q, want the change", got)
	}
	if bob := h.accountByName(t, "bob"); bob.MissingSince != nil {
		t.Errorf("bob missing_since = %v, want an incremental run to mark nothing missing", bob.MissingSince)
	}
}

func TestNSSIncrementalSync_ShouldFallBackToFullWhenTheDirectoryRefusesTheFilter(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
	h.dir.put(nssAccountEntry("", "bob", 2, 2, nil))
	h.fullSync(t)
	h.dir.remove("uid=bob,ou=people,dc=test")
	h.dir.fail = func(req *ldap.SearchRequest) error {
		if strings.Contains(req.Filter, "modifyTimestamp") {
			return ldap.NewError(ldap.LDAPResultUnwillingToPerform, errors.New("unwilling"))
		}
		return nil
	}

	h.clock.advance(15 * time.Minute)
	result, err := h.svc.IncrementalSync(t.Context())
	if err != nil {
		t.Fatalf("IncrementalSync() error = %v", err)
	}
	if !result.Full || h.accountByName(t, "bob").MissingSince == nil {
		t.Errorf("result = %+v, want a full run that marks bob missing", result)
	}
}

func TestNSSIncrementalSync_ShouldClearACollisionWhenTheOtherEntryChanges(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 3000, 3000, map[string][]string{"modifyTimestamp": {"20261017110000Z"}}))
	h.dir.put(nssAccountEntry("", "eve", 3000, 3000, map[string][]string{"modifyTimestamp": {"20261017110000Z"}}))
	h.fullSync(t)
	if h.accountByName(t, "ada").WithheldAt == nil {
		t.Fatal("ada not withheld after the collision")
	}

	h.clock.advance(15 * time.Minute)
	h.dir.put(nssAccountEntry("", "eve", 3001, 3001, map[string][]string{"modifyTimestamp": {"20261017120500Z"}}))
	if _, err := h.svc.IncrementalSync(t.Context()); err != nil {
		t.Fatalf("IncrementalSync() error = %v", err)
	}

	if ada := h.accountByName(t, "ada"); ada.WithheldAt != nil {
		t.Errorf("ada = %+v, want it served once eve carries another uid", ada)
	}
	if eve := h.accountByName(t, "eve"); eve.UID != 3001 || eve.WithheldAt != nil {
		t.Errorf("eve = %+v, want it stored with its new uid", eve)
	}
}

func TestNSSFullSync_ShouldReleaseAStaleDNWhenAnotherEntryTakesIt(t *testing.T) {
	t.Parallel()

	cfg := nssTestConfig()
	cfg.LDAP.IDAttribute = "entryUUID"
	h := newNSSHarness(t, cfg)
	h.dir.put(nssAccountEntry("cn=desk,ou=people,dc=test", "ada", 1, 1, map[string][]string{"entryUUID": {"id-ada"}}))
	h.fullSync(t)

	h.dir.put(nssAccountEntry("cn=desk,ou=people,dc=test", "bob", 2, 2, map[string][]string{"entryUUID": {"id-bob"}}))
	h.clock.advance(time.Hour)
	h.fullSync(t)

	if ada := h.accountByName(t, "ada"); ada.DN != "" || ada.MissingSince == nil {
		t.Errorf("ada = %+v, want its stale DN released and the row missing", ada)
	}
	if bob := h.accountByName(t, "bob"); bob.DN != "cn=desk,ou=people,dc=test" {
		t.Errorf("bob = %+v, want the DN", bob)
	}
}

func TestNSSSync_ShouldRefuseASecondRunWhenOneIsRunning(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.svc.syncing.Store(true)
	if _, err := h.svc.FullSync(t.Context()); !errors.Is(err, ErrNSSSyncInProgress) {
		t.Errorf("FullSync() error = %v, want ErrNSSSyncInProgress", err)
	}
	if !h.svc.SyncRunning() {
		t.Error("SyncRunning() = false, want true")
	}
}

func TestNSSDirectoryService_ShouldDoNothingWhenNSSIsDisabled(t *testing.T) {
	t.Parallel()

	var svc *NSSDirectoryService
	ctx := t.Context()
	checks := []struct {
		name string
		run  func() (bool, error)
	}{
		{"should report no user by name when nil", func() (bool, error) { return svc.RefreshUserByName(ctx, "ada") }},
		{"should report no user by uid when nil", func() (bool, error) { return svc.RefreshUserByUID(ctx, 1) }},
		{"should report no group by name when nil", func() (bool, error) { return svc.RefreshGroupByName(ctx, "ops") }},
		{"should report no group by gid when nil", func() (bool, error) { return svc.RefreshGroupByGID(ctx, 1) }},
	}
	for _, c := range checks {
		if found, err := c.run(); found || err != nil {
			t.Errorf("%s: = %v, %v; want false, nil", c.name, found, err)
		}
	}
	if result, err := svc.FullSync(ctx); result != nil || err != nil {
		t.Errorf("FullSync() = %v, %v; want nil, nil", result, err)
	}
	if n, err := svc.PurgeTombstones(ctx); n != 0 || err != nil {
		t.Errorf("PurgeTombstones() = %d, %v; want 0, nil", n, err)
	}
	if svc.SyncRunning() {
		t.Error("SyncRunning() = true on a nil service")
	}
	svc.SetAuditor(nil)
	svc.linkLogin(ctx, nil, nil, "u", &ldapEntry{})
	var nf interface{ HTTPStatusCode() int }
	if _, _, err := svc.PendingIDChanges(ctx); !errors.As(err, &nf) || nf.HTTPStatusCode() != 404 {
		t.Errorf("PendingIDChanges() error = %v, want a 404", err)
	}
}
