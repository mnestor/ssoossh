package service

// Test methodology: the viewer runs over a real database (testdb, so make
// test-postgres runs the state SQL and the searches on Postgres too) with
// rows seeded the way the sync leaves them, at the fixed clock. Every
// service is built with a refresher that fails the test if called, which
// proves the viewer never asks the directory.

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/test/testdb"
)

// forbiddenNSSRefresher fails the test on any call: the viewer must never
// reach the directory.
type forbiddenNSSRefresher struct{ t *testing.T }

// fail reports the call.
func (f forbiddenNSSRefresher) fail(call string) (bool, error) {
	f.t.Errorf("the viewer called the directory refresher: %s", call)
	return false, nil
}

// RefreshUserByName fails the test.
func (f forbiddenNSSRefresher) RefreshUserByName(_ context.Context, name string) (bool, error) {
	return f.fail("user-name:" + name)
}

// RefreshUserByUID fails the test.
func (f forbiddenNSSRefresher) RefreshUserByUID(_ context.Context, uid uint32) (bool, error) {
	return f.fail(fmt.Sprintf("user-uid:%d", uid))
}

// RefreshGroupByName fails the test.
func (f forbiddenNSSRefresher) RefreshGroupByName(_ context.Context, name string) (bool, error) {
	return f.fail("group-name:" + name)
}

// RefreshGroupByGID fails the test.
func (f forbiddenNSSRefresher) RefreshGroupByGID(_ context.Context, gid uint32) (bool, error) {
	return f.fail(fmt.Sprintf("group-gid:%d", gid))
}

// newTestNSSViewer builds the lookup service as the viewer, at the fixed
// clock, with a refresher that fails the test if called.
func newTestNSSViewer(t *testing.T, c *config.Config, db *gorm.DB) *NSSLookupService {
	t.Helper()
	return newTestNSSLookup(c, db, forbiddenNSSRefresher{t: t})
}

// firstPage is the default first page with query q.
func firstPage(q string) paging.Params {
	return paging.Params{Limit: paging.DefaultLimit, Query: q}
}

func TestNSSAccountState_ShouldPickTheFirstMarkWhenSeveralHold(t *testing.T) {
	t.Parallel()

	at := nssAt(-time.Hour)
	tests := []struct {
		name     string
		row      model.PosixUser
		disabled *time.Time
		want     model.NSSEntryState
	}{
		{name: "should be active when no mark is set", want: model.NSSEntryStateActive},
		{name: "should be withheld when every mark is set", row: model.PosixUser{WithheldAt: at, DeletedAt: at, DirDisabled: true, MissingSince: at, PendingUID: u32p(1)}, disabled: at, want: model.NSSEntryStateWithheld},
		{name: "should be a tombstone when deleted and missing", row: model.PosixUser{DeletedAt: at, MissingSince: at, DirDisabled: true}, want: model.NSSEntryStateTombstone},
		{name: "should be disabled when the directory flags it and it is missing", row: model.PosixUser{DirDisabled: true, MissingSince: at}, want: model.NSSEntryStateDisabled},
		{name: "should be disabled when the linked person is disabled", row: model.PosixUser{PendingGID: u32p(2)}, disabled: at, want: model.NSSEntryStateDisabled},
		{name: "should be missing when missing with a pending uid", row: model.PosixUser{MissingSince: at, PendingUID: u32p(1)}, want: model.NSSEntryStateMissing},
		{name: "should be pending when only the gid is pending", row: model.PosixUser{PendingGID: u32p(2)}, want: model.NSSEntryStatePending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := nssAccountState(&tt.row, tt.disabled); got != tt.want {
				t.Errorf("nssAccountState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNSSGroupState_ShouldPickTheFirstMarkWhenSeveralHold(t *testing.T) {
	t.Parallel()

	at := nssAt(-time.Hour)
	tests := []struct {
		name string
		row  model.PosixGroup
		want model.NSSEntryState
	}{
		{name: "should be active when no mark is set", want: model.NSSEntryStateActive},
		{name: "should be withheld when every mark is set", row: model.PosixGroup{WithheldAt: at, DeletedAt: at, MissingSince: at, PendingGID: u32p(1)}, want: model.NSSEntryStateWithheld},
		{name: "should be a tombstone when deleted and missing", row: model.PosixGroup{DeletedAt: at, MissingSince: at}, want: model.NSSEntryStateTombstone},
		{name: "should be missing when missing with a pending gid", row: model.PosixGroup{MissingSince: at, PendingGID: u32p(1)}, want: model.NSSEntryStateMissing},
		{name: "should be pending when the gid is pending", row: model.PosixGroup{PendingGID: u32p(1)}, want: model.NSSEntryStatePending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := nssGroupState(&tt.row); got != tt.want {
				t.Errorf("nssGroupState() = %q, want %q", got, tt.want)
			}
		})
	}
}

// u32p returns a pointer to v.
func u32p(v uint32) *uint32 { return &v }

// nssMarkCombo is one combination of an account's marks.
type nssMarkCombo struct {
	withheld, deleted, dirDisabled, missing, pendingUID, pendingGID bool
	// person is 0 for no linked person, 1 for an enabled one and 2 for a
	// disabled one.
	person int
}

// everyAccountMarkCombo is every combination of the account marks.
func everyAccountMarkCombo() []nssMarkCombo {
	var combos []nssMarkCombo
	for bits := range 1 << 6 {
		for person := range 3 {
			combos = append(combos, nssMarkCombo{
				withheld: bits&1 != 0, deleted: bits&2 != 0, dirDisabled: bits&4 != 0,
				missing: bits&8 != 0, pendingUID: bits&16 != 0, pendingGID: bits&32 != 0, person: person,
			})
		}
	}
	return combos
}

// numberedCombos numbers every account mark combination from 0, as the
// uint32 the seeded uid is built from.
func numberedCombos() map[uint32]nssMarkCombo {
	out := map[uint32]nssMarkCombo{}
	var n uint32
	for _, combo := range everyAccountMarkCombo() {
		out[n] = combo
		n++
	}
	return out
}

// seedAccountCombo writes one account carrying combo's marks, numbered n,
// and returns the state nssAccountState derives for it.
func seedAccountCombo(t *testing.T, db *gorm.DB, n uint32, combo nssMarkCombo) (string, model.NSSEntryState) {
	t.Helper()
	at := nssAt(-time.Hour)
	row := model.PosixUser{Name: fmt.Sprintf("acct%03d", n), UID: 10000 + n, GID: new(uint32(100)), DirDisabled: combo.dirDisabled}
	var personDisabled *time.Time
	if combo.person > 0 {
		if combo.person == 2 {
			personDisabled = at
		}
		id := seedNSSPerson(t, db, uuid.NewString(), row.Name, personDisabled)
		row.UserID = &id
	}
	if combo.withheld {
		row.WithheldAt = at
	}
	if combo.deleted {
		row.DeletedAt = at
	}
	if combo.missing {
		row.MissingSince = at
	}
	if combo.pendingUID {
		row.PendingUID = u32p(50000 + n)
	}
	if combo.pendingGID {
		row.PendingGID = u32p(200)
	}
	stored := seedPosixUser(t, db, row)
	return stored.ID, nssAccountState(&stored, personDisabled)
}

func TestNSSAccountStateSQL_ShouldAgreeWithTheGoStateWhenGivenEveryCombinationOfMarks(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	want := map[string]model.NSSEntryState{}
	for n, combo := range numberedCombos() {
		id, state := seedAccountCombo(t, db, n, combo)
		want[id] = state
	}

	var rows []struct {
		ID    string `gorm:"column:id"`
		State string `gorm:"column:state"`
	}
	if err := db.Table("posix_users").Select("posix_users.id AS id, " + nssAccountStateSQL + " AS state").
		Joins("LEFT JOIN users ON users.id = posix_users.user_id").Scan(&rows).Error; err != nil {
		t.Fatalf("select the state SQL: %v", err)
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(rows), len(want))
	}
	for _, r := range rows {
		if model.NSSEntryState(r.State) != want[r.ID] {
			t.Errorf("row %s: SQL state %q, Go state %q", r.ID, r.State, want[r.ID])
		}
	}
}

func TestNSSGroupStateSQL_ShouldAgreeWithTheGoStateWhenGivenEveryCombinationOfMarks(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	at := nssAt(-time.Hour)
	want := map[string]model.NSSEntryState{}
	for bits := range 1 << 4 {
		row := model.PosixGroup{Name: fmt.Sprintf("grp%02d", bits), GID: uint32(5000 + bits)}
		if bits&1 != 0 {
			row.WithheldAt = at
		}
		if bits&2 != 0 {
			row.DeletedAt = at
		}
		if bits&4 != 0 {
			row.MissingSince = at
		}
		if bits&8 != 0 {
			row.PendingGID = u32p(uint32(6000 + bits))
		}
		stored := seedPosixGroup(t, db, row)
		want[stored.ID] = nssGroupState(&stored)
	}

	var rows []struct {
		ID    string `gorm:"column:id"`
		State string `gorm:"column:state"`
	}
	if err := db.Table("posix_groups").Select("posix_groups.id AS id, " + nssGroupStateSQL + " AS state").Scan(&rows).Error; err != nil {
		t.Fatalf("select the state SQL: %v", err)
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(rows), len(want))
	}
	for _, r := range rows {
		if model.NSSEntryState(r.State) != want[r.ID] {
			t.Errorf("row %s: SQL state %q, Go state %q", r.ID, r.State, want[r.ID])
		}
	}
}

func TestNSSViewerAccounts_ShouldFilterByStateWhenGivenEveryCombinationOfMarks(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	want := map[model.NSSEntryState]int64{}
	for n, combo := range numberedCombos() {
		_, state := seedAccountCombo(t, db, n, combo)
		want[state]++
	}
	s := newTestNSSViewer(t, nssLookupTestConfig(), db)

	states := []model.NSSEntryState{
		model.NSSEntryStateWithheld, model.NSSEntryStateTombstone, model.NSSEntryStateDisabled,
		model.NSSEntryStateMissing, model.NSSEntryStatePending, model.NSSEntryStateActive,
	}
	for _, state := range states {
		page, err := s.Accounts(t.Context(), NSSViewerFilter{Page: paging.Params{Limit: paging.MaxLimit}, State: state})
		if err != nil {
			t.Fatalf("Accounts(%s): %v", state, err)
		}
		if page.Total != want[state] || want[state] == 0 {
			t.Errorf("state %s: total %d, want %d (and more than none)", state, page.Total, want[state])
		}
		for _, a := range page.Accounts {
			if a.State != state {
				t.Errorf("state %s: listed %s in state %s", state, a.Account.Name, a.State)
			}
		}
	}
}

func TestNSSViewerGroups_ShouldFilterByStateWhenTheMarksDiffer(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPosixGroup(t, db, model.PosixGroup{Name: "live", GID: 1})
	seedPosixGroup(t, db, model.PosixGroup{Name: "moving", GID: 2, PendingGID: u32p(3)})
	seedPosixGroup(t, db, model.PosixGroup{Name: "gone", GID: 4, MissingSince: nssAt(-time.Hour), DeletedAt: nssAt(-time.Hour)})
	s := newTestNSSViewer(t, nssLookupTestConfig(), db)

	tests := []struct {
		name  string
		state model.NSSEntryState
		want  []string
	}{
		{name: "should list every group when no state is given", want: []string{"gone", "live", "moving"}},
		{name: "should list only the active group when asked for active", state: model.NSSEntryStateActive, want: []string{"live"}},
		{name: "should list only the pending group when asked for pending", state: model.NSSEntryStatePending, want: []string{"moving"}},
		{name: "should list only the tombstone when asked for tombstone", state: model.NSSEntryStateTombstone, want: []string{"gone"}},
		{name: "should list nothing when no group has the state", state: model.NSSEntryStateWithheld, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := s.Groups(t.Context(), NSSViewerFilter{Page: firstPage(""), State: tt.state})
			if err != nil {
				t.Fatalf("Groups: %v", err)
			}
			got := []string{}
			for _, g := range page.Groups {
				got = append(got, g.Group.Name)
			}
			if !reflect.DeepEqual(got, tt.want) || page.Total != int64(len(tt.want)) {
				t.Errorf("groups = %v (total %d), want %v", got, page.Total, tt.want)
			}
		})
	}
}

func TestNSSViewerAccounts_ShouldMatchTheSearchWhenGivenANameDNOrUID(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPosixUser(t, db, model.PosixUser{Name: "ada", UID: 1001, GID: new(uint32(100)), DN: "uid=ada,ou=Engineering,dc=example"})
	seedPosixUser(t, db, model.PosixUser{Name: "bob_ops", UID: 2002, GID: new(uint32(100)), DN: "uid=bob_ops,ou=ops,dc=example"})
	seedPosixUser(t, db, model.PosixUser{Name: "bobXops", UID: 3003, GID: new(uint32(100001)), DN: "uid=bobXops,ou=ops,dc=example"})
	s := newTestNSSViewer(t, nssLookupTestConfig(), db)

	tests := []struct {
		name string
		q    string
		want []string
	}{
		{name: "should match a substring of the name when searched", q: "da", want: []string{"ada"}},
		{name: "should match a substring of the DN whatever its case when searched", q: "engineering", want: []string{"ada"}},
		{name: "should match the name whatever its case when searched", q: "ADA", want: []string{"ada"}},
		{name: "should match the exact uid when the search is a number", q: "2002", want: []string{"bob_ops"}},
		{name: "should not match a uid prefix when the search is a number", q: "200", want: []string{}},
		{name: "should not match the gid when the search is a number", q: "100001", want: []string{}},
		{name: "should match an underscore literally when searched", q: "b_o", want: []string{"bob_ops"}},
		{name: "should list every account when the search is empty", q: "", want: []string{"ada", "bobXops", "bob_ops"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := s.Accounts(t.Context(), NSSViewerFilter{Page: firstPage(tt.q)})
			if err != nil {
				t.Fatalf("Accounts: %v", err)
			}
			got := []string{}
			for _, a := range page.Accounts {
				got = append(got, a.Account.Name)
			}
			// The order of bobXops and bob_ops is the backend's collation's;
			// only the set matters to the search.
			if !sameNames(got, tt.want) || page.Total != int64(len(tt.want)) {
				t.Errorf("accounts = %v (total %d), want %v", got, page.Total, tt.want)
			}
		})
	}
}

// sameNames reports whether got and want hold the same names in any order.
func sameNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, n := range got {
		seen[n]++
	}
	for _, n := range want {
		seen[n]--
	}
	for _, c := range seen {
		if c != 0 {
			return false
		}
	}
	return true
}

func TestNSSViewerGroups_ShouldMatchTheSearchWhenGivenANameDNOrGID(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPosixGroup(t, db, model.PosixGroup{Name: "staff", GID: 500, DN: "cn=staff,ou=Teams,dc=example"})
	seedPosixGroup(t, db, model.PosixGroup{Name: "wheel", GID: 10})
	s := newTestNSSViewer(t, nssLookupTestConfig(), db)

	tests := []struct {
		name string
		q    string
		want []string
	}{
		{name: "should match the name whatever its case when searched", q: "STA", want: []string{"staff"}},
		{name: "should match the DN whatever its case when searched", q: "ou=teams", want: []string{"staff"}},
		{name: "should match the exact gid when the search is a number", q: "10", want: []string{"wheel"}},
		{name: "should match nothing when no group has the term", q: "nobody", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := s.Groups(t.Context(), NSSViewerFilter{Page: firstPage(tt.q)})
			if err != nil {
				t.Fatalf("Groups: %v", err)
			}
			got := []string{}
			for _, g := range page.Groups {
				got = append(got, g.Group.Name)
			}
			if !reflect.DeepEqual(got, tt.want) || page.Total != int64(len(tt.want)) {
				t.Errorf("groups = %v (total %d), want %v", got, page.Total, tt.want)
			}
		})
	}
}

func TestNSSViewerAccounts_ShouldPageByNameWhenTheListIsLong(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	for i, name := range []string{"dan", "ada", "cy", "bea", "eve"} {
		seedPosixUser(t, db, model.PosixUser{Name: name, UID: uint32(1000 + i), GID: new(uint32(100))})
	}
	s := newTestNSSViewer(t, nssLookupTestConfig(), db)

	var got []string
	after := ""
	for pages := 0; pages < 5; pages++ {
		page, err := s.Accounts(t.Context(), NSSViewerFilter{Page: paging.Params{Limit: 2, After: after}})
		if err != nil {
			t.Fatalf("Accounts: %v", err)
		}
		if page.Total != 5 {
			t.Errorf("total = %d, want 5 on every page", page.Total)
		}
		for _, a := range page.Accounts {
			got = append(got, a.Account.Name)
		}
		if page.NextCursor == nil {
			break
		}
		after = *page.NextCursor
	}
	if want := []string{"ada", "bea", "cy", "dan", "eve"}; !reflect.DeepEqual(got, want) {
		t.Errorf("paged = %v, want %v", got, want)
	}
}

func TestNSSViewerLists_ShouldRefuseTheCursorWhenItNamesNoRow(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	account := seedPosixUser(t, db, model.PosixUser{Name: "ada", UID: 1001, GID: new(uint32(100))})
	seedPosixGroup(t, db, model.PosixGroup{Name: "staff", GID: 500})
	s := newTestNSSViewer(t, nssLookupTestConfig(), db)

	tests := []struct {
		name string
		call func() error
	}{
		{name: "should refuse an accounts cursor when no account has the id", call: func() error {
			_, err := s.Accounts(t.Context(), NSSViewerFilter{Page: paging.Params{Limit: 1, After: "nope"}})
			return err
		}},
		{name: "should refuse a groups cursor when it is an account's id", call: func() error {
			_, err := s.Groups(t.Context(), NSSViewerFilter{Page: paging.Params{Limit: 1, After: account.ID}})
			return err
		}},
		{name: "should refuse a members cursor when it is not a uid", call: func() error {
			_, err := s.GroupMembers(t.Context(), 500, paging.Params{Limit: 1, After: "1x"})
			return err
		}},
		{name: "should refuse a members cursor when it is beyond uint32", call: func() error {
			_, err := s.GroupMembers(t.Context(), 500, paging.Params{Limit: 1, After: "4294967296"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertHTTPStatus(t, tt.call(), http.StatusBadRequest)
		})
	}
}

func TestNSSViewerAccount_ShouldShowWhatIsServedWhenTheAccountIsLookedUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		row           func(t *testing.T, db *gorm.DB) model.PosixUser
		private       config.AutoPrivateGroupsMode
		noGID         bool
		wantState     model.NSSEntryState
		wantStatus    string
		wantNotServed model.NSSNotServedReason
	}{
		{
			name:      "should serve an active account when nothing disables it",
			row:       func(*testing.T, *gorm.DB) model.PosixUser { return model.PosixUser{} },
			wantState: model.NSSEntryStateActive, wantStatus: apitypes.NSSStatusActive,
		},
		{
			name: "should serve a disabled account when the linked person is disabled",
			row: func(t *testing.T, db *gorm.DB) model.PosixUser {
				id := seedNSSPerson(t, db, "sub-alice", "alice", nssAt(-time.Hour))
				return model.PosixUser{UserID: &id}
			},
			wantState: model.NSSEntryStateDisabled, wantStatus: apitypes.NSSStatusDisabled,
		},
		{
			name: "should serve a deleted account when the tombstone is inside the retention",
			row: func(*testing.T, *gorm.DB) model.PosixUser {
				return model.PosixUser{MissingSince: nssAt(-48 * time.Hour), DeletedAt: nssAt(-24 * time.Hour)}
			},
			wantState: model.NSSEntryStateTombstone, wantStatus: apitypes.NSSStatusDeleted,
		},
		{
			name: "should not serve the account when it is withheld",
			row: func(*testing.T, *gorm.DB) model.PosixUser {
				return model.PosixUser{WithheldAt: nssAt(-time.Hour), DeletedAt: nssAt(-3000 * time.Hour)}
			},
			wantState: model.NSSEntryStateWithheld, wantNotServed: model.NSSNotServedWithheld,
		},
		{
			name: "should not serve the account when the tombstone is past the retention",
			row: func(*testing.T, *gorm.DB) model.PosixUser {
				return model.PosixUser{DeletedAt: nssAt(-2160*time.Hour - time.Second)}
			},
			wantState: model.NSSEntryStateTombstone, wantNotServed: model.NSSNotServedPastRetention,
		},
		{
			name: "should not serve the account when a directory group holds its private group",
			row: func(t *testing.T, db *gorm.DB) model.PosixUser {
				seedPosixGroup(t, db, model.PosixGroup{Name: "alice", GID: 777})
				return model.PosixUser{}
			},
			private:   config.AutoPrivateGroupsOn,
			wantState: model.NSSEntryStateActive, wantNotServed: model.NSSNotServedPrivateGroupClash,
		},
		{
			name:      "should not serve the account when it has no gid and the mode is hybrid",
			row:       func(*testing.T, *gorm.DB) model.PosixUser { return model.PosixUser{} },
			private:   config.AutoPrivateGroupsHybrid,
			noGID:     true,
			wantState: model.NSSEntryStateActive, wantNotServed: model.NSSNotServedNoGID,
		},
		{
			name:      "should not serve the account when it has no gid and the mode is false",
			row:       func(*testing.T, *gorm.DB) model.PosixUser { return model.PosixUser{} },
			private:   config.AutoPrivateGroupsOff,
			noGID:     true,
			wantState: model.NSSEntryStateActive, wantNotServed: model.NSSNotServedNoGID,
		},
		{
			name:      "should serve the account when it has no gid and the mode is true",
			row:       func(*testing.T, *gorm.DB) model.PosixUser { return model.PosixUser{} },
			private:   config.AutoPrivateGroupsOn,
			noGID:     true,
			wantState: model.NSSEntryStateActive, wantStatus: apitypes.NSSStatusActive,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			row := tt.row(t, db)
			row.Name, row.UID, row.GID = "alice", 10001, new(uint32(10001))
			if tt.noGID {
				row.GID = nil
			}
			seedPosixUser(t, db, row)
			c := nssLookupTestConfig()
			if tt.private != "" {
				c.NSS.AutoPrivateGroups = tt.private
			}

			view, err := newTestNSSViewer(t, c, db).Account(t.Context(), 10001)
			if err != nil {
				t.Fatalf("Account: %v", err)
			}
			if view.State != tt.wantState || view.NotServed != tt.wantNotServed {
				t.Errorf("state %q, not served %q; want %q, %q", view.State, view.NotServed, tt.wantState, tt.wantNotServed)
			}
			if (view.Served == nil) != (tt.wantNotServed != "") {
				t.Fatalf("served = %+v with not served %q; want exactly one of them", view.Served, view.NotServed)
			}
			if view.Served != nil && (view.Served.Status != tt.wantStatus || view.Served.UID != 10001) {
				t.Errorf("served = %+v, want status %q", view.Served, tt.wantStatus)
			}
		})
	}
}

func TestNSSViewerAccount_ShouldMatchTheLookupWhenTheAccountIsServed(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	id := seedNSSPerson(t, db, "sub-alice", "alice", nil)
	seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: new(uint32(500)), UserID: &id, ShellPref: ptr("/bin/zsh"), Gecos: "Alice"})
	seedPosixGroup(t, db, model.PosixGroup{Name: "staff", GID: 500})
	seedPosixGroup(t, db, model.PosixGroup{Name: "ops", GID: 600})
	seedPosixMembers(t, db, 600, 10001)
	c := nssLookupTestConfig()

	view, err := newTestNSSViewer(t, c, db).Account(t.Context(), 10001)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	lookedUp, err := newTestNSSLookup(c, db, nil).UserByUID(t.Context(), 10001)
	if err != nil {
		t.Fatalf("UserByUID: %v", err)
	}
	if !reflect.DeepEqual(view.Served, lookedUp) {
		t.Errorf("served = %+v, want the lookup's %+v", view.Served, lookedUp)
	}
	if view.Person == nil || view.Person.ID != id || view.Person.Username != "alice" || view.Person.DisabledAt != nil {
		t.Errorf("person = %+v, want the linked alice", view.Person)
	}
}

func TestNSSViewerAccount_ShouldListEveryMembershipRowWhenSomeGroupsAreNotStored(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: new(uint32(10001))})
	seedPosixGroup(t, db, model.PosixGroup{Name: "ops", GID: 600, PendingGID: u32p(601)})
	seedPosixGroup(t, db, model.PosixGroup{Name: "old", GID: 400, DeletedAt: nssAt(-time.Hour)})
	seedPosixMembers(t, db, 600, 10001)
	seedPosixMembers(t, db, 400, 10001)
	seedPosixMembers(t, db, 900, 10001)

	view, err := newTestNSSViewer(t, nssLookupTestConfig(), db).Account(t.Context(), 10001)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	want := []NSSStoredMembership{
		{GID: 400, Stored: true, Name: "old", State: model.NSSEntryStateTombstone},
		{GID: 600, Stored: true, Name: "ops", State: model.NSSEntryStatePending},
		{GID: 900},
	}
	if !reflect.DeepEqual(view.Groups, want) {
		t.Errorf("groups = %+v, want %+v", view.Groups, want)
	}
	if view.Person != nil {
		t.Errorf("person = %+v, want none for an unlinked account", view.Person)
	}
}

func TestNSSViewerLookups_ShouldAnswerNotFoundWhenNothingIsStored(t *testing.T) {
	t.Parallel()

	s := newTestNSSViewer(t, nssLookupTestConfig(), testdb.New(t))
	_, accountErr := s.Account(t.Context(), 4242)
	_, groupErr := s.Group(t.Context(), 4242)
	_, membersErr := s.GroupMembers(t.Context(), 4242, firstPage(""))
	for name, err := range map[string]error{"account": accountErr, "group": groupErr, "members": membersErr} {
		t.Run("should answer not found for the "+name+" when nothing is stored", func(t *testing.T) {
			assertHTTPStatus(t, err, http.StatusNotFound)
		})
	}
}

func TestNSSViewerGroup_ShouldShowWhatIsServedWhenTheGroupIsLookedUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		row           model.PosixGroup
		maxMembers    int
		wantNotServed model.NSSNotServedReason
		wantMembers   int
		wantTruncated bool
	}{
		{name: "should serve every member when under the cap", maxMembers: 10, wantMembers: 3},
		{name: "should serve the first members truncated when over the cap", maxMembers: 2, wantMembers: 2, wantTruncated: true},
		{name: "should serve no members when the group is a tombstone inside the retention", row: model.PosixGroup{DeletedAt: nssAt(-time.Hour)}, maxMembers: 10},
		{name: "should not serve the group when it is withheld", row: model.PosixGroup{WithheldAt: nssAt(-time.Hour)}, maxMembers: 10, wantNotServed: model.NSSNotServedWithheld},
		{name: "should not serve the group when the tombstone is past the retention", row: model.PosixGroup{DeletedAt: nssAt(-2161 * time.Hour)}, maxMembers: 10, wantNotServed: model.NSSNotServedPastRetention},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			for i, name := range []string{"ada", "bea", "cy"} {
				seedPosixUser(t, db, model.PosixUser{Name: name, UID: uint32(1000 + i), GID: new(uint32(100))})
			}
			row := tt.row
			row.Name, row.GID = "staff", 500
			seedPosixGroup(t, db, row)
			// Three stored members and one with no stored account.
			seedPosixMembers(t, db, 500, 1000, 1001, 1002, 4242)
			c := nssLookupTestConfig()
			c.NSS.MaxGroupMembers = tt.maxMembers

			view, err := newTestNSSViewer(t, c, db).Group(t.Context(), 500)
			if err != nil {
				t.Fatalf("Group: %v", err)
			}
			if view.MemberCount != 4 || view.NotServed != tt.wantNotServed {
				t.Errorf("member count %d, not served %q; want 4, %q", view.MemberCount, view.NotServed, tt.wantNotServed)
			}
			if (view.Served == nil) != (tt.wantNotServed != "") {
				t.Fatalf("served = %+v with not served %q; want exactly one of them", view.Served, view.NotServed)
			}
			if view.Served != nil && (len(view.Served.Members) != tt.wantMembers || view.Served.MembersTruncated != tt.wantTruncated) {
				t.Errorf("served = %+v, want %d members, truncated %v", view.Served, tt.wantMembers, tt.wantTruncated)
			}
		})
	}
}

func TestNSSViewerGroups_ShouldCountTheMembershipRowsWhenListed(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPosixGroup(t, db, model.PosixGroup{Name: "empty", GID: 1})
	seedPosixGroup(t, db, model.PosixGroup{Name: "staff", GID: 500})
	seedPosixMembers(t, db, 500, 1, 2, 3)

	page, err := newTestNSSViewer(t, nssLookupTestConfig(), db).Groups(t.Context(), NSSViewerFilter{Page: firstPage("")})
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	got := map[string]int64{}
	for _, g := range page.Groups {
		got[g.Group.Name] = g.MemberCount
	}
	if want := map[string]int64{"empty": 0, "staff": 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

func TestNSSViewerGroupMembers_ShouldDescribeEachMemberWhenListed(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	id := seedNSSPerson(t, db, "sub-bea", "bea", nssAt(-time.Hour))
	seedPosixUser(t, db, model.PosixUser{Name: "ada", UID: 1000, GID: new(uint32(100))})
	seedPosixUser(t, db, model.PosixUser{Name: "bea", UID: 1001, GID: new(uint32(100)), UserID: &id})
	seedPosixUser(t, db, model.PosixUser{Name: "cy", UID: 1002, GID: new(uint32(100)), WithheldAt: nssAt(-time.Hour)})
	seedPosixGroup(t, db, model.PosixGroup{Name: "staff", GID: 500})
	seedPosixMembers(t, db, 500, 1002, 4242, 1000, 1001)
	s := newTestNSSViewer(t, nssLookupTestConfig(), db)

	var got []NSSGroupMember
	after := ""
	for pages := 0; pages < 4; pages++ {
		page, err := s.GroupMembers(t.Context(), 500, paging.Params{Limit: 3, After: after})
		if err != nil {
			t.Fatalf("GroupMembers: %v", err)
		}
		if page.Total != 4 {
			t.Errorf("total = %d, want 4 on every page", page.Total)
		}
		got = append(got, page.Members...)
		if page.NextCursor == nil {
			break
		}
		after = *page.NextCursor
	}
	want := []NSSGroupMember{
		{UID: 1000, Stored: true, Name: "ada", State: model.NSSEntryStateActive},
		{UID: 1001, Stored: true, Name: "bea", State: model.NSSEntryStateDisabled},
		{UID: 1002, Stored: true, Name: "cy", State: model.NSSEntryStateWithheld},
		{UID: 4242},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("members = %+v, want %+v", got, want)
	}
}

func TestNSSViewer_ShouldNotWriteWhenEveryReadRuns(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPosixUser(t, db, model.PosixUser{Name: "alice", UID: 10001, GID: new(uint32(10001)), WithheldAt: nssAt(-time.Hour)})
	seedPosixGroup(t, db, model.PosixGroup{Name: "staff", GID: 500, DeletedAt: nssAt(-3000 * time.Hour)})
	seedPosixMembers(t, db, 500, 10001)
	s := newTestNSSViewer(t, nssLookupTestConfig(), db)
	before := nssTableSnapshot(t, db)

	ctx := t.Context()
	if _, err := s.Accounts(ctx, NSSViewerFilter{Page: firstPage("alice")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Account(ctx, 10001); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Groups(ctx, NSSViewerFilter{Page: firstPage("")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Group(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GroupMembers(ctx, 500, firstPage("")); err != nil {
		t.Fatal(err)
	}
	// Not found too: a host's miss is where the lookup would ask the
	// directory, and the refresher fails the test if the viewer does.
	_, _ = s.Account(ctx, 4242)
	_, _ = s.Group(ctx, 4242)

	if after := nssTableSnapshot(t, db); !reflect.DeepEqual(before, after) {
		t.Errorf("rows changed: before %v, after %v", before, after)
	}
}

// nssTableSnapshot counts the rows of every table the NSS directory and
// the audit log keep, and the newest update of the POSIX rows.
func nssTableSnapshot(t *testing.T, db *gorm.DB) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, table := range []string{"posix_users", "posix_groups", "posix_group_members", "nss_collisions", "audit_events", "users"} {
		var n int64
		if err := db.Table(table).Count(&n).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = n
	}
	var users []model.PosixUser
	if err := db.Find(&users).Error; err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		out["updated:"+u.ID] = u.UpdatedAt.Unix()
	}
	return out
}
