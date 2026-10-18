package service

// Test methodology: the same imported directory as nss_audit_test.go (ada
// uid 1, bob uid 2, ops gid 700 with ada), one change and one pass, then
// the nss_audit_refs rows of the events of one action are read back and
// compared, by kind and row name, with the rows the events are about. Row
// names are resolved through ids taken both before and after the pass, so
// a purged row and a row the pass created both resolve.

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/paging"
)

// nssRefName is one ref as the tests compare it: its kind and the name of
// the row it names.
type nssRefName struct {
	kind, name string
}

// rowNames maps every stored account and group id to its kind and name.
func (h *nssHarness) rowNames(t *testing.T) map[string]nssRefName {
	t.Helper()
	out := map[string]nssRefName{}
	var accounts []model.PosixUser
	if err := h.db.Find(&accounts).Error; err != nil {
		t.Fatalf("read accounts: %v", err)
	}
	for _, a := range accounts {
		out[a.ID] = nssRefName{model.NSSAuditRefAccount, a.Name}
	}
	var groups []model.PosixGroup
	if err := h.db.Find(&groups).Error; err != nil {
		t.Fatalf("read groups: %v", err)
	}
	for _, g := range groups {
		out[g.ID] = nssRefName{model.NSSAuditRefGroup, g.Name}
	}
	return out
}

// actionRefs reads the refs of every event with action, resolved through
// names, as a sorted set.
func (h *nssHarness) actionRefs(t *testing.T, action AuditAction, names map[string]nssRefName) []nssRefName {
	t.Helper()
	var refs []model.NSSAuditRef
	if err := h.db.Where("event_id IN (?)", h.db.Model(&model.AuditEvent{}).Select("id").Where("action = ?", string(action))).
		Find(&refs).Error; err != nil {
		t.Fatalf("read refs: %v", err)
	}
	set := map[nssRefName]bool{}
	for _, r := range refs {
		n, ok := names[r.RowID]
		if !ok {
			n = nssRefName{r.Kind, "unknown row " + r.RowID}
		}
		if n.kind != r.Kind {
			n.name += " (kind " + r.Kind + ")"
		}
		set[n] = true
	}
	return sortedRefNames(set)
}

// sortedRefNames is set's members in a fixed order.
func sortedRefNames(set map[nssRefName]bool) []nssRefName {
	return slices.SortedFunc(maps.Keys(set), func(a, b nssRefName) int {
		if a.kind != b.kind {
			return compareStrings(a.kind, b.kind)
		}
		return compareStrings(a.name, b.name)
	})
}

// compareStrings orders two strings.
func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// account and group are wanted refs.
func account(name string) nssRefName { return nssRefName{model.NSSAuditRefAccount, name} }
func group(name string) nssRefName   { return nssRefName{model.NSSAuditRefGroup, name} }

// nssTombstoneAll misses ada, bob and ops and keeps them missing past
// nss.tombstone_after, so all three are tombstones.
func nssTombstoneAll(t *testing.T, h *nssHarness) {
	t.Helper()
	nssMissAll(t, h)
	h.clock.advance(169 * time.Hour)
	h.fullSync(t)
}

func TestNSSAuditRefs_ShouldReferToEveryStoredRowWhenAnEventConcernsIt(t *testing.T) {
	t.Parallel()

	full := func(t *testing.T, h *nssHarness) {
		t.Helper()
		h.clock.advance(time.Hour)
		h.fullSync(t)
	}
	opsWith := func(members ...string) func(*testing.T, *nssHarness) {
		return func(_ *testing.T, h *nssHarness) {
			h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops", 700, nssChanged(nil), members...))
		}
	}
	acceptAda := func(t *testing.T, h *nssHarness) {
		t.Helper()
		full(t, h)
		if _, err := h.svc.AcceptUserIDChange(t.Context(), 1, nssAdminActor()); err != nil {
			t.Fatalf("AcceptUserIDChange() error = %v", err)
		}
	}
	tests := []struct {
		name   string
		change func(t *testing.T, h *nssHarness)
		pass   func(t *testing.T, h *nssHarness)
		action AuditAction
		want   []nssRefName
	}{
		{
			name:   "should refer to the account when nss.account_created is recorded",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssAccountEntry("", "cy", 3, 3, nil)) },
			pass:   full, action: AuditNSSAccountCreated, want: []nssRefName{account("cy")},
		},
		{
			name:   "should refer to the group when nss.group_created is recorded",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssGroupEntry("dev", 701, "ada", "bob")) },
			pass:   full, action: AuditNSSGroupCreated, want: []nssRefName{group("dev")},
		},
		{
			name: "should refer to the account when nss.account_renamed is recorded",
			change: func(_ *testing.T, h *nssHarness) {
				h.dir.put(nssAccountEntry("uid=ada,ou=people,dc=test", "ada.l", 1, 1, nil))
			},
			pass: full, action: AuditNSSAccountRenamed, want: []nssRefName{account("ada.l")},
		},
		{
			name:   "should refer to the group when nss.group_renamed is recorded",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops2", 700, nil, "ada")) },
			pass:   full, action: AuditNSSGroupRenamed, want: []nssRefName{group("ops2")},
		},
		{
			name:   "should refer to the account when nss.account_missing is recorded",
			change: func(_ *testing.T, h *nssHarness) { h.dir.remove("uid=bob,ou=people,dc=test") },
			pass:   full, action: AuditNSSAccountMissing, want: []nssRefName{account("bob")},
		},
		{
			name:   "should refer to the group when nss.group_missing is recorded",
			change: func(_ *testing.T, h *nssHarness) { h.dir.remove(nssRenameOldDN) },
			pass:   full, action: AuditNSSGroupMissing, want: []nssRefName{group("ops")},
		},
		{
			name: "should refer to each account when nss.account_tombstoned is recorded", change: nssMissAll,
			pass:   func(t *testing.T, h *nssHarness) { t.Helper(); h.clock.advance(169 * time.Hour); h.fullSync(t) },
			action: AuditNSSAccountTombstoned, want: []nssRefName{account("ada"), account("bob")},
		},
		{
			name: "should refer to the group when nss.group_tombstoned is recorded", change: nssMissAll,
			pass:   func(t *testing.T, h *nssHarness) { t.Helper(); h.clock.advance(169 * time.Hour); h.fullSync(t) },
			action: AuditNSSGroupTombstoned, want: []nssRefName{group("ops")},
		},
		{
			name:   "should refer to each account when nss.account_revived is recorded",
			change: func(t *testing.T, h *nssHarness) { t.Helper(); nssMissAll(t, h); nssRestoreAll(h) },
			pass:   full, action: AuditNSSAccountRevived, want: []nssRefName{account("ada"), account("bob")},
		},
		{
			name:   "should refer to the group when nss.group_revived is recorded",
			change: func(t *testing.T, h *nssHarness) { t.Helper(); nssTombstoneAll(t, h); nssRestoreAll(h) },
			pass:   full, action: AuditNSSGroupRevived, want: []nssRefName{group("ops")},
		},
		{
			name: "should refer to the group and the added account when a member joins", change: opsWith("ada", "bob"),
			pass: full, action: AuditNSSGroupMembersChanged, want: []nssRefName{account("bob"), group("ops")},
		},
		{
			name: "should refer to the group and both accounts when a member is swapped", change: opsWith("bob"),
			pass: full, action: AuditNSSGroupMembersChanged, want: []nssRefName{account("ada"), account("bob"), group("ops")},
		},
		{
			name:   "should refer to the group and the removed account when a tombstone wipes the members",
			change: func(_ *testing.T, h *nssHarness) { h.dir.remove(nssRenameOldDN) },
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				full(t, h)
				h.clock.advance(169 * time.Hour)
				h.fullSync(t)
			},
			action: AuditNSSGroupMembersChanged, want: []nssRefName{account("ada"), group("ops")},
		},
		{
			name:   "should refer to the account when nss.id_change_pending is recorded for it",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssAccountEntry("", "ada", 11, 1, nil)) },
			pass:   full, action: AuditNSSIDChangePending, want: []nssRefName{account("ada")},
		},
		{
			name:   "should refer to the group when nss.id_change_pending is recorded for it",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops", 701, nil, "ada")) },
			pass:   full, action: AuditNSSIDChangePending, want: []nssRefName{group("ops")},
		},
		{
			name:   "should refer to the account when its uid change is accepted",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssAccountEntry("", "ada", 11, 1, nil)) },
			pass:   acceptAda, action: AuditNSSIDChangeAccepted, want: []nssRefName{account("ada")},
		},
		{
			name:   "should refer to the group and the re-keyed account once when a uid change moves it",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssAccountEntry("", "ada", 11, 1, nil)) },
			pass:   acceptAda, action: AuditNSSGroupMembersChanged, want: []nssRefName{account("ada"), group("ops")},
		},
		{
			name:   "should refer to the group when its gid change is accepted",
			change: func(_ *testing.T, h *nssHarness) { h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops", 701, nil, "ada")) },
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				full(t, h)
				if _, err := h.svc.AcceptGroupIDChange(t.Context(), 700, nssAdminActor()); err != nil {
					t.Fatalf("AcceptGroupIDChange() error = %v", err)
				}
			},
			action: AuditNSSIDChangeAccepted, want: []nssRefName{group("ops")},
		},
		{
			name: "should refer to the purged account when its tombstone is purged", change: nssTombstoneAll,
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				if _, err := h.svc.PurgeUserTombstone(t.Context(), 1, nssAdminActor()); err != nil {
					t.Fatalf("PurgeUserTombstone() error = %v", err)
				}
			},
			action: AuditNSSTombstonePurged, want: []nssRefName{account("ada")},
		},
		{
			name: "should refer to the purged group when its tombstone is purged", change: nssTombstoneAll,
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				if _, err := h.svc.PurgeGroupTombstone(t.Context(), 700, nssAdminActor()); err != nil {
					t.Fatalf("PurgeGroupTombstone() error = %v", err)
				}
			},
			action: AuditNSSTombstonePurged, want: []nssRefName{group("ops")},
		},
		{
			name: "should refer to the group and the purged account when a purge drops its memberships",
			change: func(t *testing.T, h *nssHarness) {
				t.Helper()
				h.dir.remove("uid=ada,ou=people,dc=test")
				full(t, h)
				h.clock.advance(169 * time.Hour)
			},
			pass: func(t *testing.T, h *nssHarness) {
				t.Helper()
				h.fullSync(t)
				if _, err := h.svc.PurgeUserTombstone(t.Context(), 1, nssAdminActor()); err != nil {
					t.Fatalf("PurgeUserTombstone() error = %v", err)
				}
			},
			action: AuditNSSGroupMembersChanged, want: []nssRefName{account("ada"), group("ops")},
		},
		{
			name: "should refer to the holder when a new entry collides with its uid",
			change: func(_ *testing.T, h *nssHarness) {
				h.dir.put(nssAccountEntry("uid=eve,ou=people,dc=test", "eve", 1, 1, nil))
			},
			pass: full, action: AuditNSSUIDCollision, want: []nssRefName{account("ada")},
		},
		{
			name: "should refer to both stored rows when a rename targets a held name",
			change: func(_ *testing.T, h *nssHarness) {
				h.dir.put(nssAccountEntry("uid=bob,ou=people,dc=test", "ada", 2, 2, nil))
			},
			pass: full, action: AuditNSSNameCollision, want: []nssRefName{account("ada"), account("bob")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSAuditHarness(t)
			tt.change(t, h)
			names := h.rowNames(t)
			tt.pass(t, h)
			maps.Copy(names, h.rowNames(t))

			if got := h.actionRefs(t, tt.action, names); !slices.Equal(got, tt.want) {
				t.Errorf("%s refs = %v, want %v", tt.action, got, tt.want)
			}
		})
	}
}

func TestNSSAuditRefs_ShouldReferToEveryMemberWhenMoreThanTheListCapChange(t *testing.T) {
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

	got := h.actionRefs(t, AuditNSSGroupMembersChanged, h.rowNames(t))
	want := map[nssRefName]bool{group("ops"): true, account("ada"): true}
	for _, name := range names {
		want[account(name)] = true
	}
	if !slices.Equal(got, sortedRefNames(want)) {
		t.Errorf("refs = %d of them, want the group, ada and all 150 added accounts (%d)", len(got), len(want))
	}
}

func TestNSSAuditRefs_ShouldWriteNoRefsWhenATableIsImported(t *testing.T) {
	t.Parallel()

	h := newNSSAuditHarness(t)
	if n := h.count(t, &model.NSSAuditRef{}, ""); n != 0 {
		t.Errorf("refs after the import = %d, want none: nss.initial_import is a summary", n)
	}
}

func TestNSSAuditRefs_ShouldReferToTheAccountWhenANameMismatchIsRaised(t *testing.T) {
	t.Parallel()

	h := newNSSLinkHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
	h.fullSync(t)
	userID := seedPerson(t, h.db, "u-ada", "Ada")
	h.login(t, userID, "Ada")

	if got := h.actionRefs(t, AuditNSSNameMismatch, h.rowNames(t)); !slices.Equal(got, []nssRefName{account("ada")}) {
		t.Errorf("nss.name_mismatch refs = %v, want ada", got)
	}
}

func TestNSSAuditRefs_ShouldReferToTheAccountWhenItsShellChanges(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	aliceID := seedNSSPerson(t, h.db, "sub-alice", "alice", nil)
	row := seedPosixUser(t, h.db, model.PosixUser{Name: "alice", UID: 10001, GID: 10001, UserID: &aliceID})
	c := nssLookupTestConfig()
	lookups := newTestNSSLookup(c, h.db, nil)
	lookups.SetAuditor(NewAuditService(c, h.db))
	shell := c.NSS.AllowedShells[0]
	if _, err := lookups.SetShellPref(t.Context(), &Identity{Subject: "sub-alice", Username: "alice"}, &shell, "192.0.2.7"); err != nil {
		t.Fatalf("SetShellPref() error = %v", err)
	}

	if got := h.actionRefs(t, AuditNSSShellChanged, map[string]nssRefName{row.ID: account("alice")}); !slices.Equal(got, []nssRefName{account("alice")}) {
		t.Errorf("nss.shell_changed refs = %v, want alice", got)
	}
}

// historyActions reads every page of a row's history, newest first, as
// actions.
func historyActions(t *testing.T, read func(params paging.Params) (*AuditPage, error)) []AuditAction {
	t.Helper()
	var out []AuditAction
	params := paging.Params{Limit: 2}
	for range 20 {
		page, err := read(params)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		for _, e := range page.Events {
			out = append(out, e.Event.Action)
		}
		if page.NextCursor == nil {
			return out
		}
		params.After = *page.NextCursor
	}
	t.Fatal("history did not end")
	return nil
}

func TestNSSHistory_ShouldKeepARowsHistoryWhenItsNameOrNumberChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		steps func(t *testing.T, h *nssHarness)
		read  func(t *testing.T, l *NSSLookupService, p paging.Params) (*AuditPage, error)
		want  []AuditAction
	}{
		{
			name: "should list the account's events before and after a rename on one history",
			steps: func(t *testing.T, h *nssHarness) {
				t.Helper()
				h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops", 700, nil))
				h.clock.advance(time.Hour)
				h.fullSync(t)
				h.dir.put(nssAccountEntry("uid=ada,ou=people,dc=test", "ada.l", 1, 1, nil))
				h.clock.advance(time.Hour)
				h.fullSync(t)
			},
			read: func(t *testing.T, l *NSSLookupService, p paging.Params) (*AuditPage, error) {
				return l.AccountHistory(t.Context(), 1, p, nssAdminActor())
			},
			want: []AuditAction{AuditNSSAccountRenamed, AuditNSSGroupMembersChanged},
		},
		{
			name: "should list the group's events before and after a rename on one history",
			steps: func(t *testing.T, h *nssHarness) {
				t.Helper()
				h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops", 700, nil, "ada", "bob"))
				h.clock.advance(time.Hour)
				h.fullSync(t)
				h.dir.put(nssGroupEntryAt(nssRenameOldDN, "ops2", 700, nil, "ada", "bob"))
				h.clock.advance(time.Hour)
				h.fullSync(t)
			},
			read: func(t *testing.T, l *NSSLookupService, p paging.Params) (*AuditPage, error) {
				return l.GroupHistory(t.Context(), 700, p, nssAdminActor())
			},
			want: []AuditAction{AuditNSSGroupRenamed, AuditNSSGroupMembersChanged},
		},
		{
			name: "should list the account's events under its new uid when a uid change is accepted",
			steps: func(t *testing.T, h *nssHarness) {
				t.Helper()
				h.dir.put(nssAccountEntry("", "ada", 11, 1, nil))
				h.clock.advance(time.Hour)
				h.fullSync(t)
				h.clock.advance(time.Minute)
				if _, err := h.svc.AcceptUserIDChange(t.Context(), 1, nssAdminActor()); err != nil {
					t.Fatalf("AcceptUserIDChange() error = %v", err)
				}
			},
			read: func(t *testing.T, l *NSSLookupService, p paging.Params) (*AuditPage, error) {
				return l.AccountHistory(t.Context(), 11, p, nssAdminActor())
			},
			want: []AuditAction{AuditNSSIDChangeAccepted, AuditNSSGroupMembersChanged, AuditNSSIDChangePending},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSAuditHarness(t)
			tt.steps(t, h)
			lookups := newTestNSSLookup(h.svc.config, h.db, nil)
			lookups.SetAuditor(NewAuditService(h.svc.config, h.db))

			// Compared as sets: an accepted change records its events at one
			// instant, so their order is the ids'. ListForNSSRow's own test
			// pins the order.
			read := func(p paging.Params) (*AuditPage, error) { return tt.read(t, lookups, p) }
			got := historyActions(t, read)
			slices.Sort(got)
			slices.Sort(tt.want)
			if !slices.Equal(got, tt.want) {
				t.Errorf("history = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNSSHistory_ShouldAnswerNotFoundWhenNoRowHasTheNumber(t *testing.T) {
	t.Parallel()

	h := newNSSAuditHarness(t)
	lookups := newTestNSSLookup(h.svc.config, h.db, nil)
	lookups.SetAuditor(NewAuditService(h.svc.config, h.db))
	tests := []struct {
		name string
		read func() (*AuditPage, error)
	}{
		{name: "should answer not found when no account has the uid", read: func() (*AuditPage, error) {
			return lookups.AccountHistory(t.Context(), 99, paging.Params{Limit: 25}, nil)
		}},
		{name: "should answer not found when no group has the gid", read: func() (*AuditPage, error) {
			return lookups.GroupHistory(t.Context(), 99, paging.Params{Limit: 25}, nil)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.read(); !isNotFound(err) {
				t.Errorf("err = %v, want a NotFoundError", err)
			}
		})
	}
}

func TestNSSHistory_ShouldAnswerAnEmptyPageWhenNoAuditorIsWired(t *testing.T) {
	t.Parallel()

	h := newNSSAuditHarness(t)
	lookups := newTestNSSLookup(h.svc.config, h.db, nil)
	page, err := lookups.AccountHistory(t.Context(), 1, paging.Params{Limit: 25}, nil)
	if err != nil || page == nil || page.Events == nil || len(page.Events) != 0 {
		t.Errorf("AccountHistory() = %+v, %v; want an empty page", page, err)
	}
}
