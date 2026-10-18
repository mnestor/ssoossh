package service

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/server/model"
)

func TestNSSRefreshUser_ShouldStoreTheAccountAndItsGroupsWhenTheDirectoryHasIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		refresh func(*nssHarness) (bool, error)
		filter  string
	}{
		{
			name:    "should store the account when it is asked for by name",
			refresh: func(h *nssHarness) (bool, error) { return h.svc.RefreshUserByName(t.Context(), "svc-build") },
			filter:  "(&(objectClass=posixAccount)(uid=svc-build))",
		},
		{
			name:    "should store the account when it is asked for by uid",
			refresh: func(h *nssHarness) (bool, error) { return h.svc.RefreshUserByUID(t.Context(), 4100) },
			filter:  "(&(objectClass=posixAccount)(uidNumber=4100))",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssTestConfig()
			cfg.NSS.DisabledFilter = "(nsAccountLock=TRUE)"
			h := newNSSHarness(t, cfg)
			// A service account that never logs in, created since the last
			// sync, in two groups.
			h.dir.put(nssAccountEntry("uid=svc-build,ou=services,dc=test", "svc-build", 4100, 4100,
				map[string][]string{"nsAccountLock": {"TRUE"}}))
			h.dir.put(nssGroupEntry("builders", 6000, "svc-build", "someone-else"))
			h.dir.put(nssGroupEntry("deployers", 6001, "svc-build"))
			h.dir.put(nssGroupEntry("unrelated", 6002, "someone-else"))

			found, err := tt.refresh(h)
			if err != nil || !found {
				t.Fatalf("refresh = %v, %v; want true, nil", found, err)
			}

			row := h.accountByName(t, "svc-build")
			if row.UID != 4100 || !row.DirDisabled || row.UserID != nil {
				t.Errorf("row = %+v, want uid 4100, disabled by the directory, no person", row)
			}
			for _, gid := range []uint32{6000, 6001} {
				if got := h.members(t, gid); !reflect.DeepEqual(got, []uint32{4100}) {
					t.Errorf("members of %d = %v, want the account", gid, got)
				}
			}
			if n := h.count(t, &model.PosixGroup{}, "gid = ?", 6002); n != 0 {
				t.Errorf("unrelated group stored %d times, want only the account's groups", n)
			}
			searched := h.dir.searched()
			wantMembership := "(&(objectClass=posixGroup)(memberUid=svc-build))"
			if searched[0] != tt.filter || !slices.Contains(searched, wantMembership) || !slices.Contains(searched, "(nsAccountLock=TRUE)") {
				t.Errorf("searches = %v, want the lookup, the disabled check and the membership search", searched)
			}
		})
	}
}

func TestNSSRefreshUser_ShouldSearchByDNWhenTheMemberAttributeHoldsDNs(t *testing.T) {
	t.Parallel()

	cfg := nssTestConfig()
	cfg.NSS.Attributes.GroupMember = "member"
	h := newNSSHarness(t, cfg)
	h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
	h.dir.put(nssEntry("cn=ops,ou=groups,dc=test", map[string][]string{
		"objectClass": {"posixGroup"}, "cn": {"ops"}, "gidNumber": {"700"}, "member": {"uid=ada,ou=people,dc=test"},
	}))

	if found, err := h.svc.RefreshUserByName(t.Context(), "ada"); err != nil || !found {
		t.Fatalf("RefreshUserByName() = %v, %v", found, err)
	}
	if !slices.Contains(h.dir.searched(), `(&(objectClass=posixGroup)(member=uid=ada,ou=people,dc=test))`) {
		t.Errorf("searches = %v, want the membership search by DN", h.dir.searched())
	}
	if got := h.members(t, 700); !reflect.DeepEqual(got, []uint32{1}) {
		t.Errorf("members = %v, want ada", got)
	}
}

func TestNSSRefreshUser_ShouldReportAMissWhenTheNameDiffersOnlyInCase(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "mike", 1, 1, nil))

	found, err := h.svc.RefreshUserByName(t.Context(), "Mike")
	if err != nil || found {
		t.Errorf("RefreshUserByName(Mike) = %v, %v; want a miss: names match byte for byte", found, err)
	}
}

func TestNSSRefreshUser_ShouldWithholdTheHolderWhenTwoEntriesAnswer(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 3000, 3000, nil))
	h.dir.put(nssAccountEntry("", "eve", 3000, 3000, nil))

	found, err := h.svc.RefreshUserByUID(t.Context(), 3000)
	if err != nil || !found {
		t.Fatalf("RefreshUserByUID() = %v, %v; want true", found, err)
	}
	if n := h.count(t, &model.PosixUser{}, ""); n != 1 {
		t.Errorf("stored accounts = %d, want only the holder", n)
	}
	if n := h.count(t, &model.PosixUser{}, "withheld_at IS NOT NULL"); n != 1 {
		t.Errorf("withheld accounts = %d, want the holder withheld", n)
	}
	if got := h.audits(t, AuditNSSUIDCollision); got != 1 {
		t.Errorf("nss.uid_collision events = %d, want 1", got)
	}

	// The answer left nothing servable, so it is remembered like a miss.
	before := len(h.dir.searched())
	if _, err := h.svc.RefreshUserByUID(t.Context(), 3000); err != nil {
		t.Fatal(err)
	}
	if after := len(h.dir.searched()); after != before {
		t.Errorf("searches went from %d to %d, want the withheld answer remembered", before, after)
	}
}

func TestNSSRefreshUser_ShouldClearACollisionWhenTheLookupFindsOneEntry(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 3000, 3000, nil))
	h.dir.put(nssAccountEntry("", "eve", 3000, 3000, nil))
	h.fullSync(t)

	h.dir.remove("uid=eve,ou=people,dc=test")
	found, err := h.svc.RefreshUserByUID(t.Context(), 3000)
	if err != nil || !found {
		t.Fatalf("RefreshUserByUID() = %v, %v", found, err)
	}
	if ada := h.accountByName(t, "ada"); ada.WithheldAt != nil {
		t.Errorf("ada = %+v, want it served: the uid search saw every entry carrying it", ada)
	}
}

func TestNSSRefresh_ShouldRememberAMissWhenTheDirectoryHasNothing(t *testing.T) {
	t.Parallel()

	steps := []struct {
		name         string
		advance      time.Duration
		wantSearches int
	}{
		{name: "should search when the name was never asked for", wantSearches: 1},
		{name: "should not search again when asked inside a minute", advance: 59 * time.Second, wantSearches: 1},
		{name: "should search again when the minute has passed", advance: 2 * time.Second, wantSearches: 2},
	}
	h := newNSSHarness(t, nil)
	for _, step := range steps {
		h.clock.advance(step.advance)
		found, err := h.svc.RefreshUserByName(t.Context(), "nobody")
		if err != nil || found {
			t.Fatalf("%s: = %v, %v; want a miss", step.name, found, err)
		}
		if got := len(h.dir.searched()); got != step.wantSearches {
			t.Errorf("%s: searches = %d, want %d", step.name, got, step.wantSearches)
		}
	}
}

func TestNSSRefresh_ShouldNotRememberAnAnswerWhenItWasAnErrorOrServable(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.dialErr = errors.New("connection refused")
	if _, err := h.svc.RefreshGroupByName(t.Context(), "ops"); err == nil {
		t.Fatal("RefreshGroupByName() error = nil, want the outage reported")
	}

	h.dir.dialErr = nil
	h.dir.put(nssGroupEntry("ops", 700))
	for i := range 2 {
		found, err := h.svc.RefreshGroupByName(t.Context(), "ops")
		if err != nil || !found {
			t.Fatalf("call %d = %v, %v; want the group found", i, found, err)
		}
	}
	if got := len(h.dir.searched()); got != 2 {
		t.Errorf("searches = %d, want both calls to search: neither answer is remembered", got)
	}
}

func TestNSSRefreshUser_ShouldWriteNothingWhenAReadFailsHalfway(t *testing.T) {
	t.Parallel()

	h := newNSSHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
	h.dir.fail = func(req *ldap.SearchRequest) error {
		if strings.Contains(req.Filter, "posixGroup") {
			return ldap.NewError(ldap.LDAPResultBusy, errors.New("busy"))
		}
		return nil
	}

	if _, err := h.svc.RefreshUserByName(t.Context(), "ada"); err == nil {
		t.Fatal("RefreshUserByName() error = nil, want the failed membership search reported")
	}
	if n := h.count(t, &model.PosixUser{}, ""); n != 0 {
		t.Errorf("stored accounts = %d, want none", n)
	}
}

func TestNSSRefreshGroup_ShouldStoreTheGroupWithItsKnownMembersWhenTheDirectoryHasIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		refresh func(*nssHarness) (bool, error)
	}{
		{name: "should store the group when it is asked for by name", refresh: func(h *nssHarness) (bool, error) { return h.svc.RefreshGroupByName(t.Context(), "ops") }},
		{name: "should store the group when it is asked for by gid", refresh: func(h *nssHarness) (bool, error) { return h.svc.RefreshGroupByGID(t.Context(), 700) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSHarness(t, nil)
			h.dir.put(nssAccountEntry("", "ada", 1, 1, nil))
			h.fullSync(t)
			h.dir.put(nssGroupEntry("ops", 700, "ada", "not-yet-synced"))

			found, err := tt.refresh(h)
			if err != nil || !found {
				t.Fatalf("refresh = %v, %v; want true", found, err)
			}
			if got := h.members(t, 700); !reflect.DeepEqual(got, []uint32{1}) {
				t.Errorf("members = %v, want only the stored account", got)
			}
			if n := h.count(t, &model.PosixUser{}, "name = ?", "not-yet-synced"); n != 0 {
				t.Errorf("a member that is not stored got a row: %d", n)
			}
		})
	}
}

func TestNSSNegativeCache_ShouldStayBoundedWhenItFillsUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expire  bool
		wantLen int
	}{
		{name: "should sweep expired answers when the cache is full", expire: true, wantLen: 1},
		{name: "should start over when the cache is full of live answers", expire: false, wantLen: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSHarness(t, nil)
			for i := range nssNegativeCacheMax {
				h.svc.negativeStore("user/name/n"+strconv.Itoa(i), false)
			}
			if tt.expire {
				h.clock.advance(nssNegativeCacheTTL)
			}
			h.svc.negativeStore("user/name/last", false)
			if got := len(h.svc.negative); got != tt.wantLen {
				t.Errorf("cache size = %d, want %d", got, tt.wantLen)
			}
		})
	}
}
