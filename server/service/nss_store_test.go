package service

// Test methodology: the group rename cases store ada, bob and the group ops
// from a full sync, change the directory the way an operator renaming or
// moving ops would, run one pass (a full sync, an incremental one, or an
// on-demand read) against the fake directory, and read the stored row back
// by its id. The owner's field state (2026-10-18) is rebuilt row by row.

import (
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

const (
	// nssRenameOldDN is where ops lives before the change.
	nssRenameOldDN = "cn=ops,ou=groups,dc=test"
	// nssRenameOldTS and nssRenameNewTS are modifyTimestamps before the
	// first full sync and after it, so an incremental run reads only the
	// changed entries.
	nssRenameOldTS = "20261017110000Z"
	nssRenameNewTS = "20261017120500Z"
)

// nssGroupEntryAt builds a posixGroup entry at dn with memberUid members
// and extra attributes.
func nssGroupEntryAt(dn, name string, gid uint32, extra map[string][]string, members ...string) *ldap.Entry {
	attrs := map[string][]string{
		"objectClass": {"top", "posixGroup"},
		"cn":          {name},
		"gidNumber":   {strconv.FormatUint(uint64(gid), 10)},
	}
	if len(members) > 0 {
		attrs["memberUid"] = members
	}
	maps.Copy(attrs, extra)
	return nssEntry(dn, attrs)
}

// nssChanged is the extra attributes of an entry changed after the first
// full sync, plus more.
func nssChanged(more map[string][]string) map[string][]string {
	out := map[string][]string{"modifyTimestamp": {nssRenameNewTS}}
	maps.Copy(out, more)
	return out
}

// newNSSRenameHarness stores ada (uid 1), bob (uid 2) and ops (gid 700,
// ada its only member, opsExtra added) from a full sync, and returns the
// harness and ops' row.
func newNSSRenameHarness(t *testing.T, cfg *config.Config, opsExtra map[string][]string) (*nssHarness, model.PosixGroup) {
	t.Helper()
	h := newNSSHarness(t, cfg)
	old := map[string][]string{"modifyTimestamp": {nssRenameOldTS}}
	h.dir.put(nssAccountEntry("", "ada", 1, 1, old))
	h.dir.put(nssAccountEntry("", "bob", 2, 2, old))
	extra := maps.Clone(old)
	maps.Copy(extra, opsExtra)
	h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops", 700, extra, "ada"))
	h.fullSync(t)
	return h, h.groupByName(t, "ops")
}

// groupByID reads the stored group with id.
func (h *nssHarness) groupByID(t *testing.T, id string) model.PosixGroup {
	t.Helper()
	var row model.PosixGroup
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		t.Fatalf("read group %s: %v", id, err)
	}
	return row
}

// auditDetails decodes the detail of every audit row carrying action.
func (h *nssHarness) auditDetails(t *testing.T, action AuditAction) []map[string]any {
	t.Helper()
	var events []model.AuditEvent
	if err := h.db.Where("action = ?", string(action)).Order("created_at").Find(&events).Error; err != nil {
		t.Fatalf("read audit: %v", err)
	}
	out := make([]map[string]any, 0, len(events))
	for _, e := range events {
		var payload struct {
			Detail map[string]any `json:"detail"`
		}
		if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
			t.Fatalf("decode audit: %v", err)
		}
		out = append(out, payload.Detail)
	}
	return out
}

// nssPass is one read of the directory a case runs after its change.
type nssPass func(t *testing.T, h *nssHarness)

// nssPassFull is a full sync.
func nssPassFull(t *testing.T, h *nssHarness) { h.fullSync(t) }

// nssPassIncremental is an incremental sync, which must not have run as
// a full one.
func nssPassIncremental(t *testing.T, h *nssHarness) {
	t.Helper()
	result, err := h.svc.IncrementalSync(t.Context())
	if err != nil || result.Full {
		t.Fatalf("IncrementalSync() = %+v, %v; want an incremental run", result, err)
	}
}

// nssPassRefresh is an on-demand read that must reach the directory.
func nssPassRefresh(refresh func(*nssHarness) (bool, error)) nssPass {
	return func(t *testing.T, h *nssHarness) {
		t.Helper()
		if _, err := refresh(h); err != nil {
			t.Fatalf("refresh error = %v", err)
		}
	}
}

func TestNSSGroupRename_ShouldReanchorTheGroupWhenItsOldEntryHasGone(t *testing.T) {
	t.Parallel()

	byName := func(name string) nssPass {
		return nssPassRefresh(func(h *nssHarness) (bool, error) { return h.svc.RefreshGroupByName(t.Context(), name) })
	}
	byGID := nssPassRefresh(func(h *nssHarness) (bool, error) { return h.svc.RefreshGroupByGID(t.Context(), 700) })
	byAccount := nssPassRefresh(func(h *nssHarness) (bool, error) { return h.svc.RefreshUserByName(t.Context(), "ada") })
	tombstone := func(t *testing.T, h *nssHarness) {
		t.Helper()
		h.dir.remove(nssRenameOldDN)
		h.clock.advance(time.Minute)
		h.fullSync(t)
		h.clock.advance(169 * time.Hour)
		h.fullSync(t)
		if row := h.groupByName(t, "ops"); row.DeletedAt == nil {
			t.Fatalf("ops = %+v, want a tombstone before the rename", row)
		}
	}

	tests := []struct {
		name string
		cfg  func(*config.Config)
		// prepare runs before the rename, after the first full sync.
		prepare     nssPass
		newDN       string
		newName     string
		pass        nssPass
		wantVia     string
		wantPath    string
		wantMembers []uint32
		wantRevived bool
	}{
		{name: "should rename the group when a full sync no longer sees its old DN", newDN: "cn=ops-team,ou=groups,dc=test", newName: "ops-team", pass: nssPassFull, wantPath: nssViaFullSync, wantVia: nssGoneByFullSync, wantMembers: []uint32{1, 2}},
		{name: "should move the group when a full sync sees it in another OU", newDN: "cn=ops,ou=teams,dc=test", newName: "ops", pass: nssPassFull, wantPath: nssViaFullSync, wantVia: nssGoneByFullSync, wantMembers: []uint32{1, 2}},
		{name: "should rename the group when ldap.id_attribute is one groups do not carry", cfg: func(c *config.Config) { c.LDAP.IDAttribute = "sub" }, newDN: "cn=ops-team,ou=groups,dc=test", newName: "ops-team", pass: nssPassFull, wantPath: nssViaFullSync, wantVia: nssGoneByFullSync, wantMembers: []uint32{1, 2}},
		{name: "should rename the group when an incremental sync's base read finds the old DN gone", newDN: "cn=ops-team,ou=groups,dc=test", newName: "ops-team", pass: nssPassIncremental, wantPath: nssViaIncrementalSync, wantVia: nssGoneByBaseRead, wantMembers: []uint32{1, 2}},
		{name: "should rename the group when an on-demand read asks for its new name", newDN: "cn=ops-team,ou=groups,dc=test", newName: "ops-team", pass: byName("ops-team"), wantPath: nssViaOnDemand, wantVia: nssGoneByBaseRead, wantMembers: []uint32{1, 2}},
		{name: "should rename the group when an on-demand read asks for its gid", newDN: "cn=ops-team,ou=groups,dc=test", newName: "ops-team", pass: byGID, wantPath: nssViaOnDemand, wantVia: nssGoneByBaseRead, wantMembers: []uint32{1, 2}},
		// The membership search reads groups without their members, which
		// the sync sets: they carry over by gid unchanged.
		{name: "should rename the group and keep its members when an account read's membership search finds it", newDN: "cn=ops-team,ou=groups,dc=test", newName: "ops-team", pass: byAccount, wantPath: nssViaOnDemand, wantVia: nssGoneByBaseRead, wantMembers: []uint32{1}},
		{name: "should revive the tombstone when its group reappears renamed", prepare: tombstone, newDN: "cn=ops-team,ou=groups,dc=test", newName: "ops-team", pass: nssPassFull, wantPath: nssViaFullSync, wantVia: nssGoneByFullSync, wantMembers: []uint32{1, 2}, wantRevived: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssTestConfig()
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			h, before := newNSSRenameHarness(t, cfg, nil)
			if tt.prepare != nil {
				tt.prepare(t, h)
			}
			h.dir.remove(nssRenameOldDN)
			h.dir.put(nssGroupEntryAt(tt.newDN, tt.newName, 700, nssChanged(nil), "ada", "bob"))
			h.clock.advance(15 * time.Minute)

			tt.pass(t, h)

			row := h.groupByID(t, before.ID)
			if row.Name != tt.newName || row.DN != tt.newDN || row.GID != 700 {
				t.Errorf("row = %s %s gid %d, want %s %s gid 700", row.Name, row.DN, row.GID, tt.newName, tt.newDN)
			}
			if row.WithheldAt != nil || row.WithheldReason != "" || row.MissingSince != nil || row.DeletedAt != nil {
				t.Errorf("row withheld %v (%q), missing %v, deleted %v; want served", row.WithheldAt, row.WithheldReason, row.MissingSince, row.DeletedAt)
			}
			if n := h.count(t, &model.PosixGroup{}, ""); n != 1 {
				t.Errorf("group rows = %d, want the one row re-anchored", n)
			}
			if n := h.count(t, &model.NSSCollision{}, ""); n != 0 {
				t.Errorf("collisions = %d, want none", n)
			}
			if got := h.members(t, 700); !reflect.DeepEqual(got, tt.wantMembers) {
				t.Errorf("members = %v, want %v", got, tt.wantMembers)
			}
			if n := h.audits(t, AuditNSSGIDCollision); n != 0 {
				t.Errorf("nss.gid_collision events = %d, want none", n)
			}
			details := h.auditDetails(t, AuditNSSGroupRenamed)
			if len(details) != 1 {
				t.Fatalf("nss.group_renamed events = %d, want 1", len(details))
			}
			want := map[string]any{
				"table": "posix_groups", "gid": float64(700),
				"old_name": "ops", "new_name": tt.newName, "old_dn": nssRenameOldDN, "new_dn": tt.newDN,
				"detected_by": tt.wantVia, "name_blocked": false, "revived": tt.wantRevived, "via": tt.wantPath,
			}
			if !reflect.DeepEqual(details[0], want) {
				t.Errorf("nss.group_renamed detail = %v, want %v", details[0], want)
			}
		})
	}
}

func TestNSSGroupRename_ShouldWithholdTheGroupWhenTheRenameIsNotProven(t *testing.T) {
	t.Parallel()

	newEntry := nssGroupEntryAt("cn=ops-team,ou=groups,dc=test", "ops-team", 700, nssChanged(nil), "ada")
	byName := nssPassRefresh(func(h *nssHarness) (bool, error) { return h.svc.RefreshGroupByName(t.Context(), "ops-team") })
	byGID := nssPassRefresh(func(h *nssHarness) (bool, error) { return h.svc.RefreshGroupByGID(t.Context(), 700) })
	twoNew := func(h *nssHarness) {
		h.dir.remove(nssRenameOldDN)
		h.dir.put(newEntry)
		h.dir.put(nssGroupEntryAt("cn=ops-two,ou=groups,dc=test", "ops-two", 700, nssChanged(nil)))
	}
	busy := func(req *ldap.SearchRequest) error {
		if req.Scope == ldap.ScopeBaseObject && strings.EqualFold(req.BaseDN, nssRenameOldDN) {
			return ldap.NewError(ldap.LDAPResultBusy, errors.New("directory busy"))
		}
		return nil
	}

	tests := []struct {
		name     string
		cfg      func(*config.Config)
		opsExtra map[string][]string
		change   func(h *nssHarness)
		fail     func(*ldap.SearchRequest) error
		pass     nssPass
	}{
		{name: "should withhold the group when a full sync still sees its old DN", change: func(h *nssHarness) { h.dir.put(newEntry) }, pass: nssPassFull},
		{name: "should withhold the group when an incremental sync finds its old DN still there", change: func(h *nssHarness) { h.dir.put(newEntry) }, pass: nssPassIncremental},
		{name: "should withhold the group when an on-demand read finds its old DN still there", change: func(h *nssHarness) { h.dir.put(newEntry) }, pass: byName},
		{name: "should withhold the group when a full sync sees two new entries with its gid", change: twoNew, pass: nssPassFull},
		{name: "should withhold the group when an incremental sync sees two new entries with its gid", change: twoNew, pass: nssPassIncremental},
		{name: "should withhold the group when an on-demand read by name finds another entry with its gid", change: twoNew, pass: byName},
		{name: "should withhold the group when an on-demand read by gid finds two entries", change: twoNew, pass: byGID},
		{
			name:     "should withhold the group when the new entry carries a different directory_id",
			cfg:      func(c *config.Config) { c.LDAP.IDAttribute = "entryUUID" },
			opsExtra: map[string][]string{"entryUUID": {"id-ops"}},
			change: func(h *nssHarness) {
				h.dir.remove(nssRenameOldDN)
				h.dir.put(nssGroupEntryAt("cn=ops-team,ou=groups,dc=test", "ops-team", 700, nssChanged(map[string][]string{"entryUUID": {"id-other"}})))
			},
			pass: nssPassFull,
		},
		{name: "should withhold the group when the base read of its old DN fails", change: func(h *nssHarness) { h.dir.remove(nssRenameOldDN); h.dir.put(newEntry) }, fail: busy, pass: nssPassIncremental},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssTestConfig()
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			h, before := newNSSRenameHarness(t, cfg, tt.opsExtra)
			tt.change(h)
			h.dir.fail = tt.fail
			h.clock.advance(15 * time.Minute)

			tt.pass(t, h)

			row := h.groupByID(t, before.ID)
			if row.Name != "ops" || row.DN != nssRenameOldDN || row.WithheldAt == nil {
				t.Errorf("row = %s %s withheld %v, want ops left at its DN and withheld", row.Name, row.DN, row.WithheldAt)
			}
			if n := h.count(t, &model.NSSCollision{}, "kind = ? AND holder_id = ?", model.NSSCollisionKindGID, before.ID); n == 0 {
				t.Errorf("gid collisions held by ops = 0, want the new entry's")
			}
			if n := h.audits(t, AuditNSSGroupRenamed); n != 0 {
				t.Errorf("nss.group_renamed events = %d, want none", n)
			}
		})
	}
}

func TestNSSGroupRename_ShouldRenameOnALaterPassWhenTheBaseReadFailedEarlier(t *testing.T) {
	t.Parallel()

	h, before := newNSSRenameHarness(t, nil, nil)
	h.dir.remove(nssRenameOldDN)
	h.dir.put(nssGroupEntryAt("cn=ops-team,ou=groups,dc=test", "ops-team", 700, nssChanged(nil), "ada"))
	h.dir.fail = func(req *ldap.SearchRequest) error {
		if req.Scope == ldap.ScopeBaseObject {
			return ldap.NewError(ldap.LDAPResultBusy, errors.New("directory busy"))
		}
		return nil
	}
	h.clock.advance(15 * time.Minute)
	nssPassIncremental(t, h)

	h.dir.fail = nil
	h.clock.advance(time.Hour)
	h.fullSync(t)

	row := h.groupByID(t, before.ID)
	if row.Name != "ops-team" || row.WithheldAt != nil || h.count(t, &model.NSSCollision{}, "") != 0 {
		t.Errorf("row = %s withheld %v, collisions %d; want ops-team served with none", row.Name, row.WithheldAt, h.count(t, &model.NSSCollision{}, ""))
	}
}

func TestNSSGroupRename_ShouldReanchorButKeepTheOldNameWhenAnotherGroupHoldsTheNewOne(t *testing.T) {
	t.Parallel()

	h, before := newNSSRenameHarness(t, nil, nil)
	h.dir.put(nssGroupEntry("dev", 701))
	h.fullSync(t)
	dev := h.groupByName(t, "dev")

	h.dir.remove(nssRenameOldDN)
	h.dir.put(nssGroupEntryAt("cn=dev-two,ou=groups,dc=test", "dev", 700, nil, "ada"))
	h.clock.advance(time.Hour)
	h.fullSync(t)

	row, devAfter := h.groupByID(t, before.ID), h.groupByID(t, dev.ID)
	if row.Name != "ops" || row.DN != "cn=dev-two,ou=groups,dc=test" || row.WithheldAt == nil {
		t.Errorf("row = %s %s withheld %v, want the old name at the new DN, withheld", row.Name, row.DN, row.WithheldAt)
	}
	if devAfter.WithheldAt == nil {
		t.Errorf("dev withheld_at = nil, want dev withheld by the name collision")
	}
	if n := h.count(t, &model.NSSCollision{}, "kind = ? AND holder_id = ? AND other_id = ?", model.NSSCollisionKindGroupName, dev.ID, before.ID); n != 1 {
		t.Errorf("name collisions = %d, want dev holding the name against the renamed row", n)
	}
	if n := h.count(t, &model.NSSCollision{}, "kind = ?", model.NSSCollisionKindGID); n != 0 {
		t.Errorf("gid collisions = %d, want none", n)
	}
	details := h.auditDetails(t, AuditNSSGroupRenamed)
	if len(details) != 1 || details[0]["name_blocked"] != true || details[0]["new_name"] != "dev" {
		t.Errorf("nss.group_renamed details = %v, want one with name_blocked and new_name dev", details)
	}
}

func TestNSSGroupRename_ShouldHealTheFieldStateWhenTheNextPassRuns(t *testing.T) {
	t.Parallel()

	const (
		oldDN = "cn=users,ou=groups,dc=nanestor,dc=us"
		newDN = "cn=all-users,ou=groups,dc=nanestor,dc=us"
	)
	tests := []struct {
		name string
		pass func(t *testing.T, h *nssHarness)
	}{
		{name: "should heal the stuck group when a full sync runs", pass: nssPassFull},
		// The first incremental run on an instance runs as full, which is
		// what the first one after a deploy is.
		{name: "should heal the stuck group when the first incremental sync runs", pass: func(t *testing.T, h *nssHarness) {
			t.Helper()
			if _, err := h.svc.IncrementalSync(t.Context()); err != nil {
				t.Fatalf("IncrementalSync() error = %v", err)
			}
		}},
		{name: "should heal the stuck group when a host asks for it by its new name", pass: func(t *testing.T, h *nssHarness) {
			t.Helper()
			found, err := h.svc.RefreshGroupByName(t.Context(), "all-users")
			if err != nil || !found {
				t.Fatalf("RefreshGroupByName() = %v, %v; want found", found, err)
			}
		}},
		{name: "should heal the stuck group when a host asks for it by gid", pass: func(t *testing.T, h *nssHarness) {
			t.Helper()
			found, err := h.svc.RefreshGroupByGID(t.Context(), 2005)
			if err != nil || !found {
				t.Fatalf("RefreshGroupByGID() = %v, %v; want found", found, err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssTestConfig()
			cfg.LDAP.BaseDN = "dc=nanestor,dc=us"
			cfg.LDAP.IDAttribute = "sub"
			h := newNSSHarness(t, cfg)
			h.dir.put(nssAccountEntry("uid=mike,ou=people,dc=nanestor,dc=us", "mike", 2001, 2005, nil))
			h.dir.put(nssGroupEntryAt(newDN, "all-users", 2005, nil, "mike"))

			// The rows as the dev server held them: users withheld and
			// missing, its gid contested by the entry it was renamed to.
			synced := nssTestStart().Add(-48 * time.Hour)
			missing := nssTestStart().Add(-24 * time.Hour)
			stuck := model.PosixGroup{
				ID: "6d0c2f8e-users", GID: 2005, Name: "users", DirectoryID: "", DN: oldDN,
				MissingSince: &missing, WithheldAt: &missing,
				WithheldReason: "gid 2005 is also carried by " + newDN,
				CreatedAt:      synced, UpdatedAt: missing, LastSyncedAt: synced,
			}
			if err := h.db.Create(&stuck).Error; err != nil {
				t.Fatalf("create the stuck group: %v", err)
			}
			collision := model.NSSCollision{
				Kind: model.NSSCollisionKindGID, HolderID: stuck.ID, OtherDN: newDN, Value: "2005",
				OtherName: "all-users", OtherNumber: 2005, FirstSeenAt: missing, LastSeenAt: missing,
			}
			if err := h.db.Create(&collision).Error; err != nil {
				t.Fatalf("create the collision: %v", err)
			}

			tt.pass(t, h)

			row := h.groupByID(t, stuck.ID)
			if row.Name != "all-users" || row.DN != newDN || row.GID != 2005 {
				t.Errorf("row = %s %s gid %d, want all-users at %s with gid 2005", row.Name, row.DN, row.GID, newDN)
			}
			if row.WithheldAt != nil || row.WithheldReason != "" || row.MissingSince != nil {
				t.Errorf("row withheld %v (%q), missing %v; want served", row.WithheldAt, row.WithheldReason, row.MissingSince)
			}
			if n := h.count(t, &model.NSSCollision{}, ""); n != 0 {
				t.Errorf("collisions = %d, want the gid collision resolved", n)
			}
			if n := h.audits(t, AuditNSSGroupRenamed); n != 1 {
				t.Errorf("nss.group_renamed events = %d, want 1", n)
			}
		})
	}
}

func TestNSSGroupRename_ShouldRenameOnlyOnceWhenLaterPassesSeeTheGroupAgain(t *testing.T) {
	t.Parallel()

	h, before := newNSSRenameHarness(t, nil, nil)
	h.dir.remove(nssRenameOldDN)
	h.dir.put(nssGroupEntryAt("cn=ops-team,ou=groups,dc=test", "ops-team", 700, nil, "ada"))
	h.clock.advance(time.Hour)
	h.fullSync(t)
	h.clock.advance(time.Hour)
	result := h.fullSync(t)

	if result.Groups.Updated != 1 || h.groupByID(t, before.ID).Name != "ops-team" {
		t.Errorf("groups = %+v, want the re-anchored row updated in place", result.Groups)
	}
	if n := h.audits(t, AuditNSSGroupRenamed); n != 1 {
		t.Errorf("nss.group_renamed events = %d, want 1", n)
	}
}

func TestNSSAccountRename_ShouldStillWithholdTheAccountWhenItIsRenamedWithoutAnIDAttribute(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
	h.fullSync(t)
	before := h.accountByName(t, "ada")

	h.dir.remove("uid=ada,ou=people,dc=test")
	h.dir.put(nssAccountEntry("", "ada.byron", 1001, 1001, nil))
	h.clock.advance(time.Hour)
	result := h.fullSync(t)

	row := h.accountByName(t, "ada")
	if result.Accounts.Refused != 1 || row.ID != before.ID || row.WithheldAt == nil {
		t.Errorf("accounts = %+v, ada withheld %v; want the renamed entry refused and ada withheld", result.Accounts, row.WithheldAt)
	}
	if n := h.count(t, &model.PosixUser{}, "name = ?", "ada.byron"); n != 0 {
		t.Errorf("ada.byron rows = %d, want none", n)
	}
	if n := h.audits(t, AuditNSSGroupRenamed); n != 0 {
		t.Errorf("nss.group_renamed events = %d, want none", n)
	}
}

// nssGIDEntry is alice (uid 10001) with gid, or with no gid attribute when
// gid is nil.
func nssGIDEntry(gid *uint32) *ldap.Entry {
	if gid == nil {
		return nssAccountEntry("", "alice", 10001, 0, map[string][]string{"gidNumber": nil})
	}
	return nssAccountEntry("", "alice", 10001, *gid, nil)
}

func TestNSSStoreAccount_ShouldStoreTheGIDAsSSSDDoesWhenEachModeReadsTheEntry(t *testing.T) {
	t.Parallel()

	// Each read path parses and stores the same way: a full sync and an
	// on-demand read. A directory group holding the uid as its gid never
	// stops the row being stored; it decides only what is served (see the
	// lookup tests).
	equal, other := new(uint32(10001)), new(uint32(500))
	tests := []struct {
		name       string
		mode       config.AutoPrivateGroupsMode
		gid        *uint32
		holder     bool
		wantStored bool
		wantGID    *uint32
	}{
		{name: "should store a gid equal to the uid when the mode is false", mode: config.AutoPrivateGroupsOff, gid: equal, wantStored: true, wantGID: equal},
		{name: "should store a differing gid when the mode is false and a group holds the uid", mode: config.AutoPrivateGroupsOff, gid: other, holder: true, wantStored: true, wantGID: other},
		{name: "should refuse the entry when the mode is false and it has no gid", mode: config.AutoPrivateGroupsOff},
		{name: "should store a gid equal to the uid when the mode is hybrid and a group holds it", mode: config.AutoPrivateGroupsHybrid, gid: equal, holder: true, wantStored: true, wantGID: equal},
		{name: "should store a differing gid when the mode is hybrid", mode: config.AutoPrivateGroupsHybrid, gid: other, wantStored: true, wantGID: other},
		{name: "should refuse the entry when the mode is hybrid and it has no gid", mode: config.AutoPrivateGroupsHybrid, holder: true},
		{name: "should store a gid equal to the uid when the mode is true", mode: config.AutoPrivateGroupsOn, gid: equal, wantStored: true, wantGID: equal},
		{name: "should store a differing gid when the mode is true and a group holds the uid", mode: config.AutoPrivateGroupsOn, gid: other, holder: true, wantStored: true, wantGID: other},
		{name: "should store no gid when the mode is true and the entry has none", mode: config.AutoPrivateGroupsOn, wantStored: true},
	}

	reads := map[string]func(t *testing.T, h *nssHarness){
		"full sync": func(t *testing.T, h *nssHarness) { h.fullSync(t) },
		"on-demand read": func(t *testing.T, h *nssHarness) {
			if _, err := h.svc.RefreshUserByName(t.Context(), "alice"); err != nil {
				t.Fatalf("RefreshUserByName() error = %v", err)
			}
		},
	}
	for _, tt := range tests {
		for path, read := range reads {
			t.Run(tt.name+" by "+path, func(t *testing.T) {
				t.Parallel()

				cfg := nssTestConfig()
				cfg.NSS.AutoPrivateGroups = tt.mode
				h := newNSSHarness(t, cfg)
				h.dir.put(nssGIDEntry(tt.gid))
				if tt.holder {
					h.dir.put(nssGroupEntry("alice-upg", 10001))
				}
				read(t, h)

				if n := h.count(t, &model.PosixUser{}, "name = ?", "alice"); n != map[bool]int64{true: 1}[tt.wantStored] {
					t.Fatalf("stored rows = %d, want stored %v", n, tt.wantStored)
				}
				if !tt.wantStored {
					return
				}
				if row := h.accountByName(t, "alice"); !equalGID(row.GID, tt.wantGID) || row.PendingGID != nil {
					t.Errorf("gid = %s pending %s, want %s with nothing pending", gidText(row.GID), gidText(row.PendingGID), gidText(tt.wantGID))
				}
			})
		}
	}
}

func TestNSSStoreAccount_ShouldHoldOrFollowAGIDChangeWhenEachModeSyncs(t *testing.T) {
	t.Parallel()

	// stored is the row's gid (and any pending one left by another mode)
	// before the sync; directory the entry's gid then. Under "true" sssd
	// keeps the directory gid only as a supplementary group, so it is
	// followed; in the other modes it is the primary gid and frozen.
	tests := []struct {
		name        string
		mode        config.AutoPrivateGroupsMode
		stored      *uint32
		pending     *uint32
		directory   *uint32
		wantGID     *uint32
		wantPending *uint32
	}{
		{name: "should hold a changed gid pending when the mode is false", mode: config.AutoPrivateGroupsOff,
			stored: new(uint32(500)), directory: new(uint32(600)), wantGID: new(uint32(500)), wantPending: new(uint32(600))},
		{name: "should hold a changed gid pending when the mode is hybrid", mode: config.AutoPrivateGroupsHybrid,
			stored: new(uint32(500)), directory: new(uint32(600)), wantGID: new(uint32(500)), wantPending: new(uint32(600))},
		{name: "should hold a gid changed to the uid pending when the mode is hybrid", mode: config.AutoPrivateGroupsHybrid,
			stored: new(uint32(500)), directory: new(uint32(10001)), wantGID: new(uint32(500)), wantPending: new(uint32(10001))},
		{name: "should follow a changed gid when the mode is true", mode: config.AutoPrivateGroupsOn,
			stored: new(uint32(500)), directory: new(uint32(600)), wantGID: new(uint32(600))},
		{name: "should drop a gid the entry no longer has when the mode is true", mode: config.AutoPrivateGroupsOn,
			stored: new(uint32(500)), wantGID: nil},
		{name: "should keep the stored gid when the mode is hybrid and the entry lost its gid", mode: config.AutoPrivateGroupsHybrid,
			stored: new(uint32(500)), wantGID: new(uint32(500))},
		{name: "should keep the stored gid when the mode is false and the entry lost its gid", mode: config.AutoPrivateGroupsOff,
			stored: new(uint32(500)), wantGID: new(uint32(500))},
		{name: "should take the first gid when the mode is hybrid and the row has none", mode: config.AutoPrivateGroupsHybrid,
			directory: new(uint32(600)), wantGID: new(uint32(600))},
		{name: "should take the first gid when the mode is false and the row has none", mode: config.AutoPrivateGroupsOff,
			directory: new(uint32(600)), wantGID: new(uint32(600))},
		{name: "should take the first gid when the mode is true and the row has none", mode: config.AutoPrivateGroupsOn,
			directory: new(uint32(600)), wantGID: new(uint32(600))},
		{name: "should drop a pending gid left by another mode when the mode is true", mode: config.AutoPrivateGroupsOn,
			stored: new(uint32(500)), pending: new(uint32(600)), directory: new(uint32(600)), wantGID: new(uint32(600))},
		{name: "should keep an unchanged gid when the mode is true", mode: config.AutoPrivateGroupsOn,
			stored: new(uint32(500)), directory: new(uint32(500)), wantGID: new(uint32(500))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssTestConfig()
			cfg.NSS.AutoPrivateGroups = tt.mode
			h := newNSSHarness(t, cfg)
			seeded := model.PosixUser{Name: "alice", UID: 10001, DN: "uid=alice,ou=people,dc=test", GID: tt.stored, PendingGID: tt.pending}
			if tt.pending != nil {
				seeded.PendingSince = nssAt(-time.Hour)
			}
			seedPosixUser(t, h.db, seeded)
			h.dir.put(nssGIDEntry(tt.directory))
			h.fullSync(t)

			row := h.accountByName(t, "alice")
			if !equalGID(row.GID, tt.wantGID) || !equalGID(row.PendingGID, tt.wantPending) {
				t.Errorf("gid = %s pending %s, want %s pending %s", gidText(row.GID), gidText(row.PendingGID), gidText(tt.wantGID), gidText(tt.wantPending))
			}
			if (row.PendingSince != nil) != (tt.wantPending != nil) {
				t.Errorf("pending_since = %v, want set exactly when a gid is pending", row.PendingSince)
			}
			if n := h.audits(t, AuditNSSIDChangePending); (n > 0) != (tt.wantPending != nil) {
				t.Errorf("%s events = %d, want one exactly when a gid is pending", AuditNSSIDChangePending, n)
			}
		})
	}
}

func TestNSSStoreAccount_ShouldServeTheOwnersServiceAccountOnlyWhenTheModeIsTrue(t *testing.T) {
	t.Parallel()

	// sv-ssoossh (uid 9001, no gidNumber), the owner's lldap entry of
	// 2026-10-18: refused under hybrid as sssd refuses it, and under true
	// given its private group, by the sync and by a lookup's on-demand read.
	tests := []struct {
		name     string
		mode     config.AutoPrivateGroupsMode
		wantMiss bool
	}{
		{name: "should not serve it when the mode is hybrid", mode: config.AutoPrivateGroupsHybrid, wantMiss: true},
		{name: "should serve it with its private group when the mode is true", mode: config.AutoPrivateGroupsOn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssTestConfig()
			cfg.NSS.AutoPrivateGroups = tt.mode
			h := newNSSHarness(t, cfg)
			h.dir.put(nssAccountEntry("", "sv-ssoossh", 9001, 0, map[string][]string{"gidNumber": nil}))
			result := h.fullSync(t)
			lookups := NewNSSLookupService(cfg, h.db, h.svc)

			got, err := lookups.UserByName(t.Context(), "sv-ssoossh")
			if tt.wantMiss {
				wantNSSCode(t, err, apitypes.ErrorCodeNSSNotFound)
				if result.Accounts.Skipped != 1 {
					t.Errorf("skipped = %d, want the entry skipped", result.Accounts.Skipped)
				}
				return
			}
			want := []apitypes.NSSMembership{{GID: 9001, Name: "sv-ssoossh"}}
			if err != nil || got.GID != 9001 || !reflect.DeepEqual(got.Groups, want) {
				t.Errorf("UserByName() = %+v, %v; want gid 9001 and groups %+v", got, err, want)
			}
		})
	}
}

// addedAt reads the added_at of the membership of uid in gid, failing the
// test when there is no such row.
func (h *nssHarness) addedAt(t *testing.T, gid, uid uint32) *time.Time {
	t.Helper()
	var row model.PosixGroupMember
	if err := h.db.Where("gid = ? AND uid = ?", gid, uid).First(&row).Error; err != nil {
		t.Fatalf("read membership of %d in %d: %v", uid, gid, err)
	}
	return row.AddedAt
}

// nssMembership names one membership row and the added_at it should hold:
// nil for one the first full import stored.
type nssMembership struct {
	gid, uid uint32
	want     *time.Time
}

func TestNSSMemberAddedAt_ShouldRecordWhenTheMembershipWasFirstStored(t *testing.T) {
	t.Parallel()

	// importOps stores ada (uid 1) and bob (uid 2) and ops (gid 700) with
	// ada its only member, from the first full sync, then an hour later
	// adds bob to ops with a second full sync.
	importOps := func(t *testing.T, h *nssHarness) {
		t.Helper()
		h.dir.put(nssAccountEntry("", "ada", 1, 100, nil))
		h.dir.put(nssAccountEntry("", "bob", 2, 100, nil))
		h.dir.put(nssGroupEntry("ops", 700, "ada"))
		h.fullSync(t)
		h.clock.advance(time.Hour)
		h.dir.put(nssGroupEntry("ops", 700, "ada", "bob"))
		h.fullSync(t)
	}

	tests := []struct {
		name  string
		steps func(t *testing.T, h *nssHarness)
		want  []nssMembership
	}{
		{
			name: "should leave added_at null when the first full import stores the membership",
			steps: func(t *testing.T, h *nssHarness) {
				h.dir.put(nssAccountEntry("", "ada", 1, 100, nil))
				h.dir.put(nssGroupEntry("ops", 700, "ada"))
				h.fullSync(t)
			},
			want: []nssMembership{{700, 1, nil}},
		},
		{
			name:  "should set added_at when a later sync adds the membership",
			steps: importOps,
			want:  []nssMembership{{700, 1, nil}, {700, 2, nssAt(time.Hour)}},
		},
		{
			name: "should keep added_at when a later sync sees the membership unchanged",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.clock.advance(time.Hour)
				h.fullSync(t)
				h.clock.advance(time.Hour)
				if result, err := h.svc.IncrementalSync(t.Context()); err != nil || result.Full {
					t.Fatalf("IncrementalSync() = %+v, %v; want an incremental run", result, err)
				}
			},
			want: []nssMembership{{700, 1, nil}, {700, 2, nssAt(time.Hour)}},
		},
		{
			name: "should keep added_at when an accepted uid change re-keys the memberships",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.clock.advance(time.Hour)
				h.dir.put(nssAccountEntry("", "ada", 1001, 100, nil))
				h.dir.put(nssAccountEntry("", "bob", 1002, 100, nil))
				h.fullSync(t)
				for _, uid := range []uint32{1, 2} {
					if _, err := h.svc.AcceptUserIDChange(t.Context(), uid, nssAdminActor()); err != nil {
						t.Fatalf("AcceptUserIDChange(%d) error = %v", uid, err)
					}
				}
				h.clock.advance(time.Hour)
				h.fullSync(t)
			},
			want: []nssMembership{{700, 1001, nil}, {700, 1002, nssAt(time.Hour)}},
		},
		{
			name: "should keep added_at when an accepted gid change re-keys the members",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.clock.advance(time.Hour)
				h.dir.put(nssGroupEntry("ops", 800, "ada", "bob"))
				h.fullSync(t)
				if _, err := h.svc.AcceptGroupIDChange(t.Context(), 700, nssAdminActor()); err != nil {
					t.Fatalf("AcceptGroupIDChange() error = %v", err)
				}
				h.clock.advance(time.Hour)
				h.fullSync(t)
			},
			want: []nssMembership{{800, 1, nil}, {800, 2, nssAt(time.Hour)}},
		},
		{
			name: "should keep added_at when the group is renamed with the same gid",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.clock.advance(time.Hour)
				h.dir.remove("cn=ops,ou=groups,dc=test")
				h.dir.put(nssGroupEntry("ops-team", 700, "ada", "bob"))
				h.fullSync(t)
				if row := h.groupByName(t, "ops-team"); row.GID != 700 {
					t.Fatalf("ops-team = %+v, want the renamed ops", row)
				}
			},
			want: []nssMembership{{700, 1, nil}, {700, 2, nssAt(time.Hour)}},
		},
		{
			name: "should keep added_at when a tombstoned account is revived",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.dir.remove("uid=bob,ou=people,dc=test")
				h.clock.advance(time.Hour)
				h.fullSync(t)
				h.clock.advance(169 * time.Hour)
				h.fullSync(t)
				if row := h.accountByName(t, "bob"); row.DeletedAt == nil {
					t.Fatalf("bob = %+v, want a tombstone before the revive", row)
				}
				h.clock.advance(time.Hour)
				h.dir.put(nssAccountEntry("", "bob", 2, 100, nil))
				h.fullSync(t)
			},
			want: []nssMembership{{700, 1, nil}, {700, 2, nssAt(time.Hour)}},
		},
		{
			name: "should keep added_at when an on-demand account read sees the membership unchanged",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.clock.advance(time.Hour)
				if _, err := h.svc.RefreshUserByName(t.Context(), "bob"); err != nil {
					t.Fatalf("RefreshUserByName() error = %v", err)
				}
			},
			want: []nssMembership{{700, 1, nil}, {700, 2, nssAt(time.Hour)}},
		},
		{
			name: "should set added_at when an on-demand account read finds a new membership",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.clock.advance(time.Hour)
				h.dir.put(nssAccountEntry("", "carol", 3, 100, nil))
				h.dir.put(nssGroupEntry("ops", 700, "ada", "bob", "carol"))
				if _, err := h.svc.RefreshUserByName(t.Context(), "carol"); err != nil {
					t.Fatalf("RefreshUserByName() error = %v", err)
				}
			},
			want: []nssMembership{{700, 1, nil}, {700, 2, nssAt(time.Hour)}, {700, 3, nssAt(2 * time.Hour)}},
		},
		{
			name: "should reset added_at when the membership is removed and added again",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.clock.advance(time.Hour)
				h.dir.put(nssGroupEntry("ops", 700))
				h.fullSync(t)
				h.clock.advance(time.Hour)
				h.dir.put(nssGroupEntry("ops", 700, "ada", "bob"))
				h.fullSync(t)
			},
			want: []nssMembership{{700, 1, nssAt(3 * time.Hour)}, {700, 2, nssAt(3 * time.Hour)}},
		},
		{
			name: "should set added_at for every member when a group first appears after the import",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.clock.advance(time.Hour)
				h.dir.put(nssGroupEntry("devs", 900, "ada", "bob"))
				h.fullSync(t)
			},
			want: []nssMembership{{700, 1, nil}, {700, 2, nssAt(time.Hour)}, {900, 1, nssAt(2 * time.Hour)}, {900, 2, nssAt(2 * time.Hour)}},
		},
		{
			// A tombstoned group is served with no members: the tombstone
			// removes them, so a revival adds them again.
			name: "should reset added_at when a tombstoned group is revived",
			steps: func(t *testing.T, h *nssHarness) {
				importOps(t, h)
				h.dir.remove("cn=ops,ou=groups,dc=test")
				h.clock.advance(time.Hour)
				h.fullSync(t)
				h.clock.advance(169 * time.Hour)
				h.fullSync(t)
				if n := len(h.members(t, 700)); n != 0 {
					t.Fatalf("members of the tombstone = %d, want none", n)
				}
				h.clock.advance(time.Hour)
				h.dir.put(nssGroupEntry("ops", 700, "ada"))
				h.fullSync(t)
			},
			want: []nssMembership{{700, 1, nssAt(172 * time.Hour)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSHarness(t, nil)
			tt.steps(t, h)

			var stored int64
			for _, m := range tt.want {
				got := h.addedAt(t, m.gid, m.uid)
				if (got == nil) != (m.want == nil) || (got != nil && !got.Equal(*m.want)) {
					t.Errorf("added_at of %d in %d = %v, want %v", m.uid, m.gid, got, m.want)
				}
				stored++
			}
			if n := h.count(t, &model.PosixGroupMember{}, ""); n != stored {
				t.Errorf("membership rows = %d, want exactly the %d checked", n, stored)
			}
		})
	}
}
