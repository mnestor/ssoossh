package service

// Test methodology: each case imports ada (uid 1), bob (uid 2) and ops
// (gid 700, ada its member) with a full sync, which writes only the
// nss.initial_import summaries, then changes the directory and runs one
// pass, and reads back the audit rows of the one action it is about.

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

// newNSSAuditHarness is newNSSRenameHarness: the import is done, so every
// later write is audited row by row.
func newNSSAuditHarness(t *testing.T) *nssHarness {
	t.Helper()
	h, _ := newNSSRenameHarness(t, nil, nil)
	return h
}

// nssMissAll removes ada, bob and ops from the directory and runs a full
// sync, so all three are missing.
func nssMissAll(t *testing.T, h *nssHarness) {
	t.Helper()
	h.dir.remove("uid=ada,ou=people,dc=test")
	h.dir.remove("uid=bob,ou=people,dc=test")
	h.dir.remove(nssRenameOldDN)
	h.clock.advance(time.Minute)
	h.fullSync(t)
}

// nssRestoreAll puts ada, bob and ops back as the harness imported them.
func nssRestoreAll(h *nssHarness) {
	h.dir.put(nssAccountEntry("", "ada", 1, 1, nssChanged(nil)))
	h.dir.put(nssAccountEntry("", "bob", 2, 2, nssChanged(nil)))
	h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops", 700, nssChanged(nil), "ada"))
}

// subset reports whether every key of want has the same value in got.
func subset(got, want map[string]any) bool {
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			return false
		}
	}
	return true
}

func TestNSSAudit_ShouldRecordTheLifecycleEventWhenARowChanges(t *testing.T) {
	t.Parallel()

	incremental := func(t *testing.T, h *nssHarness) {
		t.Helper()
		h.clock.advance(15 * time.Minute)
		nssPassIncremental(t, h)
	}
	full := func(t *testing.T, h *nssHarness) {
		t.Helper()
		h.clock.advance(time.Hour)
		h.fullSync(t)
	}

	tests := []struct {
		name       string
		change     func(t *testing.T, h *nssHarness)
		pass       func(t *testing.T, h *nssHarness)
		action     AuditAction
		wantDetail map[string]any
	}{
		{
			name:       "should record nss.account_created when a full sync stores a new account",
			change:     func(_ *testing.T, h *nssHarness) { h.dir.put(nssAccountEntry("", "cy", 3, 3, nil)) },
			pass:       full,
			action:     AuditNSSAccountCreated,
			wantDetail: map[string]any{"table": "posix_users", "name": "cy", "uid": float64(3), "gid": float64(3), "dn": "uid=cy,ou=people,dc=test", "via": nssViaFullSync},
		},
		{
			name:       "should record nss.account_created when an incremental sync stores a new account",
			change:     func(_ *testing.T, h *nssHarness) { h.dir.put(nssAccountEntry("", "cy", 3, 3, nssChanged(nil))) },
			pass:       incremental,
			action:     AuditNSSAccountCreated,
			wantDetail: map[string]any{"name": "cy", "via": nssViaIncrementalSync},
		},
		{
			name:   "should record nss.account_created when an on-demand read stores a new account",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssAccountEntry("", "cy", 3, 3, nil)) },
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				if _, err := h.svc.RefreshUserByName(t.Context(), "cy"); err != nil {
					t.Fatalf("RefreshUserByName() error = %v", err)
				}
			},
			action:     AuditNSSAccountCreated,
			wantDetail: map[string]any{"name": "cy", "via": nssViaOnDemand},
		},
		{
			name:   "should record nss.account_created when a login reads a new account",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssAccountEntry("", "cy", 3, 3, nil)) },
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				filter := h.svc.andFilter(h.svc.config.NSS.UserFilter, "uidNumber", "3")
				if _, _, err := h.svc.readAccountsOnDemand(t.Context(), nil, filter, nssKinds{number: true}, nssViaLogin, func(a *nssAccount) bool { return a.UID == 3 }); err != nil {
					t.Fatalf("readAccountsOnDemand() error = %v", err)
				}
			},
			action:     AuditNSSAccountCreated,
			wantDetail: map[string]any{"name": "cy", "via": nssViaLogin},
		},
		{
			name:       "should record nss.group_created with its member count when a full sync stores a new group",
			change:     func(_ *testing.T, h *nssHarness) { h.dir.put(nssGroupEntry("dev", 701, "ada", "bob")) },
			pass:       full,
			action:     AuditNSSGroupCreated,
			wantDetail: map[string]any{"table": "posix_groups", "name": "dev", "gid": float64(701), "member_count": float64(2), "via": nssViaFullSync},
		},
		{
			name:   "should record nss.group_created when an on-demand read stores a new group",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssGroupEntry("dev", 701, "bob")) },
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				if _, err := h.svc.RefreshGroupByName(t.Context(), "dev"); err != nil {
					t.Fatalf("RefreshGroupByName() error = %v", err)
				}
			},
			action:     AuditNSSGroupCreated,
			wantDetail: map[string]any{"name": "dev", "member_count": float64(1), "via": nssViaOnDemand},
		},
		{
			name: "should record nss.account_renamed when a matched account takes a new name",
			change: func(_ *testing.T, h *nssHarness) {
				h.dir.put(nssAccountEntry("uid=ada,ou=people,dc=test", "ada.l", 1, 1, nil))
			},
			pass:       full,
			action:     AuditNSSAccountRenamed,
			wantDetail: map[string]any{"old_name": "ada", "new_name": "ada.l", "uid": float64(1), "via": nssViaFullSync},
		},
		{
			name:       "should record nss.group_renamed as matched when a matched group takes a new name",
			change:     func(_ *testing.T, h *nssHarness) { h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops2", 700, nil, "ada")) },
			pass:       full,
			action:     AuditNSSGroupRenamed,
			wantDetail: map[string]any{"old_name": "ops", "new_name": "ops2", "detected_by": nssGroupMatched, "via": nssViaFullSync},
		},
		{
			name:       "should record nss.account_missing when a full sync does not see an account",
			change:     func(_ *testing.T, h *nssHarness) { h.dir.remove("uid=bob,ou=people,dc=test") },
			pass:       full,
			action:     AuditNSSAccountMissing,
			wantDetail: map[string]any{"name": "bob", "uid": float64(2), "via": nssViaFullSync},
		},
		{
			name:       "should record nss.group_missing when a full sync does not see a group",
			change:     func(_ *testing.T, h *nssHarness) { h.dir.remove(nssRenameOldDN) },
			pass:       full,
			action:     AuditNSSGroupMissing,
			wantDetail: map[string]any{"name": "ops", "gid": float64(700), "via": nssViaFullSync},
		},
		{
			name:       "should record nss.account_tombstoned when an account stays missing",
			change:     nssMissAll,
			pass:       func(t *testing.T, h *nssHarness) { t.Helper(); h.clock.advance(169 * time.Hour); h.fullSync(t) },
			action:     AuditNSSAccountTombstoned,
			wantDetail: map[string]any{"name": "ada", "via": nssViaFullSync},
		},
		{
			name:       "should record nss.group_tombstoned when a group stays missing",
			change:     nssMissAll,
			pass:       func(t *testing.T, h *nssHarness) { t.Helper(); h.clock.advance(169 * time.Hour); h.fullSync(t) },
			action:     AuditNSSGroupTombstoned,
			wantDetail: map[string]any{"name": "ops", "gid": float64(700), "via": nssViaFullSync},
		},
		{
			name:       "should record nss.account_revived when a missing account reappears",
			change:     func(t *testing.T, h *nssHarness) { t.Helper(); nssMissAll(t, h); nssRestoreAll(h) },
			pass:       full,
			action:     AuditNSSAccountRevived,
			wantDetail: map[string]any{"name": "ada", "was_tombstoned": false, "via": nssViaFullSync},
		},
		{
			name: "should record nss.group_revived when a tombstoned group reappears",
			change: func(t *testing.T, h *nssHarness) {
				t.Helper()
				nssMissAll(t, h)
				h.clock.advance(169 * time.Hour)
				h.fullSync(t)
				nssRestoreAll(h)
			},
			pass:       full,
			action:     AuditNSSGroupRevived,
			wantDetail: map[string]any{"name": "ops", "was_tombstoned": true, "via": nssViaFullSync},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSAuditHarness(t)
			tt.change(t, h)
			tt.pass(t, h)

			details := h.auditDetails(t, tt.action)
			found := false
			for _, d := range details {
				found = found || subset(d, tt.wantDetail)
			}
			if !found {
				t.Errorf("%s details = %v, want one with %v", tt.action, details, tt.wantDetail)
			}
		})
	}
}

func TestNSSAudit_ShouldSetTheTargetWhenTheAccountIsLinked(t *testing.T) {
	t.Parallel()

	h := newNSSAuditHarness(t)
	userID := seedNSSPerson(t, h.db, "sub-bob", "bob", nil)
	if err := h.db.Model(&model.PosixUser{}).Where("uid = ?", 2).Update("user_id", userID).Error; err != nil {
		t.Fatalf("link bob: %v", err)
	}
	h.dir.remove("uid=bob,ou=people,dc=test")
	h.clock.advance(time.Hour)
	h.fullSync(t)

	var events []model.AuditEvent
	if err := h.db.Where("action = ?", string(AuditNSSAccountMissing)).Find(&events).Error; err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(events) != 1 || events[0].TargetUserID == nil || *events[0].TargetUserID != userID || events[0].ActorUserID != nil {
		t.Errorf("events = %+v, want one with bob's person as target and no actor", events)
	}
}

// nssMemberEvent is one nss.group_members_changed as the tests compare it.
type nssMemberEvent struct {
	name, via                string
	added, removed           []any
	addedCount, removedCount float64
}

// memberEvents reads every nss.group_members_changed.
func (h *nssHarness) memberEvents(t *testing.T) []nssMemberEvent {
	t.Helper()
	var out []nssMemberEvent
	for _, d := range h.auditDetails(t, AuditNSSGroupMembersChanged) {
		added, _ := d["added"].([]any)
		removed, _ := d["removed"].([]any)
		name, _ := d["name"].(string)
		via, _ := d["via"].(string)
		addedCount, _ := d["added_count"].(float64)
		removedCount, _ := d["removed_count"].(float64)
		out = append(out, nssMemberEvent{name: name, via: via, added: added, removed: removed, addedCount: addedCount, removedCount: removedCount})
	}
	return out
}

// member is one entry of an added or removed list as JSON decodes it.
func member(uid float64, name string) any {
	return map[string]any{"uid": uid, "name": name}
}

func TestNSSAudit_ShouldRecordOneMembersChangedEventWhenAGroupsMembersChange(t *testing.T) {
	t.Parallel()

	opsWith := func(members ...string) func(*nssHarness) {
		return func(h *nssHarness) {
			h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops", 700, nssChanged(nil), members...))
		}
	}
	tests := []struct {
		name   string
		change func(*nssHarness)
		pass   func(t *testing.T, h *nssHarness)
		want   nssMemberEvent
	}{
		{
			name: "should record bob added when a full sync sees him join", change: opsWith("ada", "bob"),
			pass: func(t *testing.T, h *nssHarness) { t.Helper(); h.fullSync(t) },
			want: nssMemberEvent{name: "ops", via: nssViaFullSync, added: []any{member(2, "bob")}, removed: []any{}, addedCount: 1},
		},
		{
			name: "should record ada swapped for bob when an incremental sync sees it", change: opsWith("bob"),
			pass: nssPassIncremental,
			want: nssMemberEvent{name: "ops", via: nssViaIncrementalSync, added: []any{member(2, "bob")}, removed: []any{member(1, "ada")}, addedCount: 1, removedCount: 1},
		},
		{
			name: "should record bob added when an on-demand group read sees him join", change: opsWith("ada", "bob"),
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				if _, err := h.svc.RefreshGroupByGID(t.Context(), 700); err != nil {
					t.Fatalf("RefreshGroupByGID() error = %v", err)
				}
			},
			want: nssMemberEvent{name: "ops", via: nssViaOnDemand, added: []any{member(2, "bob")}, removed: []any{}, addedCount: 1},
		},
		{
			name: "should record bob added when an on-demand read of his account finds the group", change: opsWith("ada", "bob"),
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				if _, err := h.svc.RefreshUserByName(t.Context(), "bob"); err != nil {
					t.Fatalf("RefreshUserByName() error = %v", err)
				}
			},
			want: nssMemberEvent{name: "ops", via: nssViaOnDemand, added: []any{member(2, "bob")}, removed: []any{}, addedCount: 1},
		},
		{
			name: "should record the members removed when a group is tombstoned",
			change: func(h *nssHarness) {
				h.dir.remove(nssRenameOldDN)
			},
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				h.fullSync(t)
				h.clock.advance(169 * time.Hour)
				h.fullSync(t)
			},
			want: nssMemberEvent{name: "ops", via: nssViaTombstoned, added: []any{}, removed: []any{member(1, "ada")}, removedCount: 1},
		},
		{
			name: "should record the account re-keyed when an admin accepts its uid change",
			change: func(h *nssHarness) {
				h.dir.put(nssAccountEntry("", "ada", 11, 1, nil))
			},
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				h.fullSync(t)
				if _, err := h.svc.AcceptUserIDChange(t.Context(), 1, nssAdminActor()); err != nil {
					t.Fatalf("AcceptUserIDChange() error = %v", err)
				}
			},
			want: nssMemberEvent{name: "ops", via: nssViaIDChangeAccepted, added: []any{member(11, "ada")}, removed: []any{member(1, "ada")}, addedCount: 1, removedCount: 1},
		},
		{
			name: "should record the account removed when its tombstone is purged",
			change: func(h *nssHarness) {
				h.dir.remove("uid=ada,ou=people,dc=test")
			},
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				h.fullSync(t)
				h.clock.advance(169 * time.Hour)
				h.fullSync(t)
				if _, err := h.svc.PurgeUserTombstone(t.Context(), 1, nssAdminActor()); err != nil {
					t.Fatalf("PurgeUserTombstone() error = %v", err)
				}
			},
			want: nssMemberEvent{name: "ops", via: nssViaTombstonePurged, added: []any{}, removed: []any{member(1, "ada")}, removedCount: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSAuditHarness(t)
			tt.change(h)
			h.clock.advance(15 * time.Minute)
			tt.pass(t, h)

			got := h.memberEvents(t)
			if len(got) != 1 || !reflect.DeepEqual(got[0], tt.want) {
				t.Errorf("nss.group_members_changed = %+v, want exactly %+v", got, tt.want)
			}
		})
	}
}

func TestNSSAudit_ShouldCapTheMemberListsWhenManyMembersChange(t *testing.T) {
	t.Parallel()

	h := newNSSAuditHarness(t)
	names := make([]string, 0, 150)
	for i := range 150 {
		name := fmt.Sprintf("u%03d", i)
		names = append(names, name)
		h.dir.put(nssAccountEntry("", name, uint32(1000+i), 1, nil))
	}
	h.clock.advance(time.Hour)
	h.fullSync(t)
	h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops", 700, nil, names...))
	h.clock.advance(time.Hour)
	h.fullSync(t)

	got := h.memberEvents(t)
	if len(got) != 1 {
		t.Fatalf("nss.group_members_changed events = %d, want 1", len(got))
	}
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "should cap the added list at 100", ok: len(got[0].added) == nssMemberListCap},
		{name: "should count every added member", ok: got[0].addedCount == 150},
		{name: "should list the lowest uids first", ok: reflect.DeepEqual(got[0].added[0], member(1000, "u000"))},
		{name: "should count ada removed", ok: got[0].removedCount == 1 && len(got[0].removed) == 1},
	}
	for _, tt := range tests {
		if !tt.ok {
			t.Errorf("%s: event = %+v", tt.name, got[0])
		}
	}
}

func TestNSSAudit_ShouldRecordOnlyTheSummaryWhenATableIsImported(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
	h.dir.put(nssAccountEntry("", "bob", 2, 2, nil))
	h.dir.put(nssGroupEntry("ops", 700, "ada", "bob"))
	h.fullSync(t)

	tests := []struct {
		name   string
		action AuditAction
		want   int64
	}{
		{name: "should record no nss.account_created", action: AuditNSSAccountCreated, want: 0},
		{name: "should record no nss.group_created", action: AuditNSSGroupCreated, want: 0},
		{name: "should record no nss.group_members_changed", action: AuditNSSGroupMembersChanged, want: 0},
		{name: "should record one nss.initial_import per table", action: AuditNSSInitialImport, want: 2},
	}
	for _, tt := range tests {
		if got := h.audits(t, tt.action); got != tt.want {
			t.Errorf("%s: %s events = %d, want %d", tt.name, tt.action, got, tt.want)
		}
	}
	details := map[any]map[string]any{}
	for _, d := range h.auditDetails(t, AuditNSSInitialImport) {
		details[d["table"]] = d
	}
	want := map[any]map[string]any{
		"posix_users":  {"table": "posix_users", "via": nssViaFullSync, "created": float64(2)},
		"posix_groups": {"table": "posix_groups", "via": nssViaFullSync, "created": float64(1), "memberships": float64(2)},
	}
	if !reflect.DeepEqual(details, want) {
		t.Errorf("nss.initial_import details = %v, want %v", details, want)
	}
}

func TestNSSAudit_ShouldRecordNothingWhenTheDirectoryHasNotChanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pass func(t *testing.T, h *nssHarness)
	}{
		{name: "should record nothing when a second full sync runs", pass: func(t *testing.T, h *nssHarness) { t.Helper(); h.fullSync(t) }},
		{name: "should record nothing when an incremental sync rereads every entry", pass: func(t *testing.T, h *nssHarness) {
			t.Helper()
			// Every entry modified since the baseline: all are read again.
			nssRestoreAll(h)
			nssPassIncremental(t, h)
		}},
		{name: "should record nothing when on-demand reads find the stored entries", pass: func(t *testing.T, h *nssHarness) {
			t.Helper()
			if _, err := h.svc.RefreshUserByName(t.Context(), "ada"); err != nil {
				t.Fatalf("RefreshUserByName() error = %v", err)
			}
			if _, err := h.svc.RefreshGroupByName(t.Context(), "ops"); err != nil {
				t.Fatalf("RefreshGroupByName() error = %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSAuditHarness(t)
			before := h.count(t, &model.AuditEvent{}, "")
			h.clock.advance(15 * time.Minute)
			tt.pass(t, h)

			if got := h.count(t, &model.AuditEvent{}, ""); got != before {
				t.Errorf("audit rows = %d, want %d: nothing changed", got, before)
			}
		})
	}
}
