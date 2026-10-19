package service

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/test/testdb"
)

// Test methodology: AdminList runs against a real database (testdb, so
// make test-postgres runs every query on Postgres too) holding a small
// fleet built by seedAdminFleet, with usage rows and alert claims written
// the way the usage flusher and the alert claim leave them. The clock is
// fixed at nssNow.

// adminFleet is the ids of the keys seedAdminFleet writes, by name.
type adminFleet map[string]string

// createAdminKey creates a key as caller, with an expiry when expires is
// non-zero, and returns its id.
func createAdminKey(t *testing.T, keys *NSSKeyService, caller *Identity, name, group string, expires time.Duration) string {
	t.Helper()

	in := NSSKeyCreate{Name: name, GroupName: group}
	if expires != 0 {
		at := nssNow.Add(expires)
		in.ExpiresAt = &at
	}
	view, _, err := keys.Create(t.Context(), caller, in, "192.0.2.1")
	if err != nil {
		t.Fatalf("create key %q: %v", name, err)
	}
	return view.Key.ID
}

// seedAdminFleet writes the fleet the admin list tests read, at nssNow:
//
//   - alpha (web-admins, kim): used from 10.0.4.7 an hour ago, 10.0.5.9 two
//     days ago and 10.0.6.1 eight days ago; a key source alert two days ago.
//   - bravo (db-admins, kim): used from 2001:db8::1 three days ago and from
//     192.0.2.50 a hundred days ago, past nss.usage_retention but not yet
//     pruned; a miss rate alert ten days ago, past the lookback.
//   - charlie (no group, kim): never used, expires in 10 days; a miss rate
//     alert six days ago.
//   - delta (web-admins, kim): expires in 5 days but revoked; a revoked
//     attempt alert an hour ago, not a usage alert.
//   - echo (ops, kim): expired a day ago, used from 198.51.100.4 forty days
//     ago.
//   - foxtrot (ops, root-admin): used from 10.0.4.7 twenty-nine days ago.
func seedAdminFleet(t *testing.T, db *gorm.DB, keys *NSSKeyService) adminFleet {
	t.Helper()

	fleet := adminFleet{
		"alpha":   createAdminKey(t, keys, nssKeyAdmin, "alpha", "web-admins", 0),
		"bravo":   createAdminKey(t, keys, nssKeyAdmin, "bravo", "db-admins", 0),
		"charlie": createAdminKey(t, keys, nssKeyAdmin, "charlie", "", 10*24*time.Hour),
		"delta":   createAdminKey(t, keys, nssKeyAdmin, "delta", "web-admins", 5*24*time.Hour),
		"echo":    createAdminKey(t, keys, nssKeyAdmin, "echo", "ops", 0),
		"foxtrot": createAdminKey(t, keys, nssAdmin, "foxtrot", "ops", 0),
	}
	if _, err := keys.Revoke(t.Context(), nssAdmin, fleet["delta"], ""); err != nil {
		t.Fatalf("revoke delta: %v", err)
	}
	if err := db.Model(&model.NSSKey{}).Where("id = ?", fleet["echo"]).
		Update("expires_at", nssNow.Add(-24*time.Hour).UTC()).Error; err != nil {
		t.Fatalf("expire echo: %v", err)
	}

	use := func(name, ip string, ago time.Duration) {
		at := nssNow.Add(-ago)
		seedNSSUsage(t, db, fleet[name], ip, 1, at.Add(-time.Minute), at, 10)
	}
	use("alpha", "10.0.4.7", time.Hour)
	use("alpha", "10.0.5.9", 48*time.Hour)
	use("alpha", "10.0.6.1", 8*24*time.Hour)
	use("bravo", "2001:db8::1", 72*time.Hour)
	use("bravo", "192.0.2.50", 100*24*time.Hour)
	use("echo", "198.51.100.4", 40*24*time.Hour)
	use("foxtrot", "10.0.4.7", 29*24*time.Hour)

	seedNSSAlert(t, db, nssAlertKeySource, fleet["alpha"], nssNow.Add(-48*time.Hour))
	seedNSSAlert(t, db, nssAlertMissRate, fleet["bravo"], nssNow.Add(-10*24*time.Hour))
	seedNSSAlert(t, db, nssAlertMissRate, fleet["charlie"], nssNow.Add(-6*24*time.Hour))
	seedNSSAlert(t, db, nssAlertRevokedAttempt, fleet["delta"], nssNow.Add(-time.Hour))
	return fleet
}

// adminListNames lists filter's keys by name, failing the test on error.
func adminListNames(t *testing.T, keys *NSSKeyService, filter NSSAdminKeyFilter) []string {
	t.Helper()

	if filter.Page.Limit == 0 {
		filter.Page.Limit = paging.MaxLimit
	}
	page, err := keys.AdminList(t.Context(), filter)
	if err != nil {
		t.Fatalf("AdminList(%+v): %v", filter, err)
	}
	names := []string{}
	for _, entry := range page.Keys {
		names = append(names, entry.Key.Name)
	}
	return names
}

// withQuery is a filter searching for q.
func withQuery(q string) NSSAdminKeyFilter {
	return NSSAdminKeyFilter{Page: paging.Params{Query: q}}
}

func TestNSSKeyAdminList_ShouldListTheMatchingKeysWhenFiltered(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	seedAdminFleet(t, db, keys)

	const day = 24 * time.Hour
	tests := []struct {
		name   string
		filter NSSAdminKeyFilter
		want   []string
	}{
		{name: "should list every key by name when nothing filters", filter: NSSAdminKeyFilter{},
			want: []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}},

		{name: "should match the name when the text is part of it", filter: withQuery("alp"), want: []string{"alpha"}},
		{name: "should match the group whatever its case when the text is part of it", filter: withQuery("DB-ADM"), want: []string{"bravo"}},
		{name: "should match the creator's username when the text is part of it", filter: withQuery("root-adm"), want: []string{"foxtrot"}},
		{name: "should match the creator's email when the text is part of it", filter: withQuery("kim@EXAMPLE"),
			want: []string{"alpha", "bravo", "charlie", "delta", "echo"}},
		{name: "should match nothing when the text is a LIKE wildcard", filter: withQuery("%"), want: []string{}},
		{name: "should match nothing when no key carries the text", filter: withQuery("bastion"), want: []string{}},

		{name: "should list the active keys when the status is active", filter: NSSAdminKeyFilter{Status: model.NSSKeyStatusActive},
			want: []string{"alpha", "bravo", "charlie", "foxtrot"}},
		{name: "should list the expired keys when the status is expired", filter: NSSAdminKeyFilter{Status: model.NSSKeyStatusExpired}, want: []string{"echo"}},
		{name: "should list the revoked keys when the status is revoked", filter: NSSAdminKeyFilter{Status: model.NSSKeyStatusRevoked}, want: []string{"delta"}},

		{name: "should list the live keys expiring in the window when it is 30 days", filter: NSSAdminKeyFilter{ExpiringWithin: 30 * day}, want: []string{"charlie"}},
		{name: "should list nothing when no live key expires within 7 days", filter: NSSAdminKeyFilter{ExpiringWithin: 7 * day}, want: []string{}},
		{name: "should list nothing when expiring keys are asked for among the expired", filter: NSSAdminKeyFilter{ExpiringWithin: 30 * day, Status: model.NSSKeyStatusExpired}, want: []string{}},

		{name: "should list the keys with no lookup in 30 days when unused is on", filter: NSSAdminKeyFilter{Unused: true},
			want: []string{"charlie", "delta", "echo"}},
		{name: "should list the keys a usage alert fired for in 7 days when alerts is on", filter: NSSAdminKeyFilter{Alerts: true},
			want: []string{"alpha", "charlie"}},
		{name: "should combine the filters when several are on", filter: NSSAdminKeyFilter{Alerts: true, Unused: true}, want: []string{"charlie"}},

		{name: "should find the key used from the address when q is an IPv4 address", filter: withQuery("10.0.4.7"), want: []string{"alpha", "foxtrot"}},
		{name: "should find the same keys when q is the address IPv4-mapped", filter: withQuery("::ffff:10.0.4.7"), want: []string{"alpha", "foxtrot"}},
		{name: "should find the key used from the address when q is an IPv6 address", filter: withQuery("2001:db8::1"), want: []string{"bravo"}},
		{name: "should find the keys used from the range when q is an IPv4 range", filter: withQuery("10.0.4.0/24"), want: []string{"alpha", "foxtrot"}},
		{name: "should find the key used from the address when q is a /32", filter: withQuery("10.0.5.9/32"), want: []string{"alpha"}},
		{name: "should find the key used from the range when q is an IPv6 range", filter: withQuery("2001:db8::/32"), want: []string{"bravo"}},
		{name: "should find every key used from IPv4 in the window when q is 0.0.0.0/0", filter: withQuery("0.0.0.0/0"), want: []string{"alpha", "echo", "foxtrot"}},
		{name: "should find every key used in the window when q is ::/0", filter: withQuery("::/0"), want: []string{"alpha", "bravo", "echo", "foxtrot"}},
		{name: "should find nothing when the range holds no usage", filter: withQuery("203.0.113.0/24"), want: []string{}},
		{name: "should find nothing when the address was never seen", filter: withQuery("10.0.4.8"), want: []string{}},
		{name: "should leave out usage older than the retention when it is not yet pruned", filter: withQuery("192.0.2.50"), want: []string{}},
		{name: "should apply the filters too when q is an address", filter: NSSAdminKeyFilter{Page: paging.Params{Query: "0.0.0.0/0"}, Status: model.NSSKeyStatusExpired},
			want: []string{"echo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adminListNames(t, keys, tt.filter); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("keys = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNSSKeyAdminList_ShouldSearchEveryRetainedRowWhenRetentionKeepsThemAll(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	c := nssLookupTestConfig()
	c.NSS.UsageRetention = 0
	keys, _ := newTestNSSKeys(t, c, db)
	seedAdminFleet(t, db, keys)

	if got := adminListNames(t, keys, withQuery("192.0.2.50")); !reflect.DeepEqual(got, []string{"bravo"}) {
		t.Errorf("keys = %v, want bravo's 100-day-old use found", got)
	}
}

func TestNSSKeyAdminList_ShouldRefuseTheSearchWhenQIsAnInvalidAddress(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)

	_, err := keys.AdminList(t.Context(), withQuery("10.0.4.0/33"))
	var invalid *errorresponses.InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("AdminList = %v, want a 400", err)
	}
}

func TestNSSKeyAdminList_ShouldDescribeEachKeyWhenListed(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	ids := seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	fleet := seedAdminFleet(t, db, keys)

	page, err := keys.AdminList(t.Context(), NSSAdminKeyFilter{Page: paging.Params{Limit: 25}})
	if err != nil {
		t.Fatalf("AdminList: %v", err)
	}
	if page.Total != 6 || page.NextCursor != nil || page.Address != nil || page.UsageRetention != 2160*time.Hour {
		t.Errorf("page = total %d, next %v, address %+v, retention %s", page.Total, page.NextCursor, page.Address, page.UsageRetention)
	}
	entries := map[string]NSSAdminKeyEntry{}
	for _, entry := range page.Keys {
		entries[entry.Key.Name] = entry
	}

	alphaSeen := nssNow.Add(-time.Hour)
	alphaAlert := nssNow.Add(-48 * time.Hour)
	charlieAlert := nssNow.Add(-6 * 24 * time.Hour)
	tests := []struct {
		name         string
		key          string
		wantStatus   model.NSSKeyStatus
		wantCreator  string
		wantEmail    string
		wantHosts    int64
		wantSeen     *time.Time
		wantMissRate *time.Time
		wantSource   *time.Time
	}{
		{name: "should count the hosts of the last 7 days when a key is in use", key: "alpha", wantStatus: model.NSSKeyStatusActive,
			wantCreator: "kim", wantEmail: "kim@example.org", wantHosts: 2, wantSeen: &alphaSeen, wantSource: &alphaAlert},
		{name: "should count no hosts when a key was used only before the week", key: "foxtrot", wantStatus: model.NSSKeyStatusActive,
			wantCreator: "root-admin", wantEmail: "root-admin@example.org", wantSeen: new(nssNow.Add(-29 * 24 * time.Hour))},
		{name: "should report no use when a key was never used", key: "charlie", wantStatus: model.NSSKeyStatusActive,
			wantCreator: "kim", wantEmail: "kim@example.org", wantMissRate: &charlieAlert},
		{name: "should derive the status when a key is revoked", key: "delta", wantStatus: model.NSSKeyStatusRevoked,
			wantCreator: "kim", wantEmail: "kim@example.org"},
		{name: "should derive the status when a key is expired", key: "echo", wantStatus: model.NSSKeyStatusExpired,
			wantCreator: "kim", wantEmail: "kim@example.org", wantSeen: new(nssNow.Add(-40 * 24 * time.Hour))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := entries[tt.key]
			if !ok {
				t.Fatalf("%s not listed", tt.key)
			}
			if got.Key.ID != fleet[tt.key] || got.Status != tt.wantStatus {
				t.Errorf("id, status = %s, %s; want %s, %s", got.Key.ID, got.Status, fleet[tt.key], tt.wantStatus)
			}
			if !got.CreatorFound || got.CreatorUsername != tt.wantCreator || got.CreatorEmail != tt.wantEmail {
				t.Errorf("creator = %v %q %q, want %q %q", got.CreatorFound, got.CreatorUsername, got.CreatorEmail, tt.wantCreator, tt.wantEmail)
			}
			wantCreatorID := ids[nssKeyAdmin.Subject]
			if tt.wantCreator == "root-admin" {
				wantCreatorID = ids[nssAdmin.Subject]
			}
			if got.Key.CreatedBy != wantCreatorID {
				t.Errorf("created_by = %s, want %s", got.Key.CreatedBy, wantCreatorID)
			}
			if got.Hosts != tt.wantHosts {
				t.Errorf("hosts = %d, want %d", got.Hosts, tt.wantHosts)
			}
			if !sameTime(got.LastSeenAt, tt.wantSeen) || !sameTime(got.MissRateAlertAt, tt.wantMissRate) || !sameTime(got.KeySourceAlertAt, tt.wantSource) {
				t.Errorf("last seen, miss rate, key source = %s, %s, %s; want %s, %s, %s",
					timeOrNil(got.LastSeenAt), timeOrNil(got.MissRateAlertAt), timeOrNil(got.KeySourceAlertAt),
					timeOrNil(tt.wantSeen), timeOrNil(tt.wantMissRate), timeOrNil(tt.wantSource))
			}
			if got.Key.SecretHash != "" || got.Key.PreviousSecretHash != nil {
				t.Error("the secret hashes were read for the list")
			}
			if got.Matched != nil {
				t.Errorf("matched = %+v, want none for a text search", got.Matched)
			}
		})
	}
}

func TestNSSKeyAdminList_ShouldNameTheMatchingAddressesWhenQIsAnAddress(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	fleet := seedAdminFleet(t, db, keys)

	// alpha is rotated, so its generation 1 rows are the previous secret;
	// 10.0.4.7 switched half an hour ago and 10.0.5.9 never did.
	if _, _, err := keys.Rotate(t.Context(), nssKeyAdmin, fleet["alpha"], ""); err != nil {
		t.Fatalf("rotate alpha: %v", err)
	}
	switched := nssNow.Add(-30 * time.Minute)
	seedNSSUsage(t, db, fleet["alpha"], "10.0.4.7", 2, switched.Add(-time.Minute), switched, 3)

	page, err := keys.AdminList(t.Context(), NSSAdminKeyFilter{Page: paging.Params{Query: "10.0.0.7/16", Limit: 25}})
	if err != nil {
		t.Fatalf("AdminList: %v", err)
	}
	if page.Address == nil || page.Address.Display != "10.0.0.0/16" || !page.Address.Range {
		t.Fatalf("address = %+v, want the range 10.0.0.0/16", page.Address)
	}
	if len(page.Keys) != 2 || page.Keys[0].Key.Name != "alpha" || page.Keys[1].Key.Name != "foxtrot" {
		t.Fatalf("keys = %+v, want alpha and foxtrot", page.Keys)
	}

	tests := []struct {
		name string
		got  *NSSKeyAddressMatches
		want NSSKeyAddressMatches
	}{
		{
			name: "should list each address once, newest first, with the secret it last presented when a key matches several",
			got:  page.Keys[0].Matched,
			want: NSSKeyAddressMatches{Total: 3, Addresses: []NSSKeyAddressMatch{
				{SourceIP: "10.0.4.7", LastSeen: switched, Generation: 2, Secret: model.NSSSecretGenerationCurrent},
				{SourceIP: "10.0.5.9", LastSeen: nssNow.Add(-48 * time.Hour), Generation: 1, Secret: model.NSSSecretGenerationPrevious},
				{SourceIP: "10.0.6.1", LastSeen: nssNow.Add(-8 * 24 * time.Hour), Generation: 1, Secret: model.NSSSecretGenerationPrevious},
			}},
		},
		{
			name: "should list the one address when a key matches once",
			got:  page.Keys[1].Matched,
			want: NSSKeyAddressMatches{Total: 1, Addresses: []NSSKeyAddressMatch{
				{SourceIP: "10.0.4.7", LastSeen: nssNow.Add(-29 * 24 * time.Hour), Generation: 1, Secret: model.NSSSecretGenerationCurrent},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got == nil {
				t.Fatal("matched = nil, want the matching addresses")
			}
			if tt.got.Total != tt.want.Total || len(tt.got.Addresses) != len(tt.want.Addresses) {
				t.Fatalf("matched = %+v, want %+v", tt.got, tt.want)
			}
			for i, want := range tt.want.Addresses {
				got := tt.got.Addresses[i]
				if got.SourceIP != want.SourceIP || !got.LastSeen.Equal(want.LastSeen) || got.Generation != want.Generation || got.Secret != want.Secret {
					t.Errorf("address %d = %+v, want %+v", i, got, want)
				}
			}
		})
	}
}

func TestNSSKeyAdminList_ShouldBoundTheAddressesWhenAKeyMatchesMany(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	id := createAdminKey(t, keys, nssKeyAdmin, "busy", "web-admins", 0)
	for i := range 7 {
		at := nssNow.Add(-time.Duration(i+1) * time.Hour)
		seedNSSUsage(t, db, id, "10.1.0."+string(rune('1'+i)), 1, at.Add(-time.Minute), at, 1)
	}

	page, err := keys.AdminList(t.Context(), NSSAdminKeyFilter{Page: paging.Params{Query: "10.1.0.0/24", Limit: 25}})
	if err != nil {
		t.Fatalf("AdminList: %v", err)
	}
	if len(page.Keys) != 1 || page.Keys[0].Matched == nil {
		t.Fatalf("keys = %+v, want busy with its matches", page.Keys)
	}
	matched := page.Keys[0].Matched
	got := []string{}
	for _, use := range matched.Addresses {
		got = append(got, use.SourceIP)
	}
	if want := []string{"10.1.0.1", "10.1.0.2", "10.1.0.3", "10.1.0.4", "10.1.0.5"}; matched.Total != 7 || !reflect.DeepEqual(got, want) {
		t.Errorf("matched = %v of %d, want %v of 7", got, matched.Total, want)
	}
}

func TestNSSKeyAdminList_ShouldKeepTheCreatorIDWhenTheCreatorNoLongerExists(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	testdb.WithoutForeignKeys(t, db, func(tx *gorm.DB) error {
		return tx.Create(&model.NSSKey{
			ID: "nss_orphan23", CurrentGeneration: 1, SecretHash: "00", Name: "orphan", CreatedBy: "gone-user",
			CreatedAt: nssNow, UpdatedAt: nssNow,
		}).Error
	})

	page, err := keys.AdminList(t.Context(), NSSAdminKeyFilter{Page: paging.Params{Limit: 25}})
	if err != nil {
		t.Fatalf("AdminList: %v", err)
	}
	if len(page.Keys) != 1 {
		t.Fatalf("keys = %+v, want the orphan", page.Keys)
	}
	got := page.Keys[0]
	if got.CreatorFound || got.Key.CreatedBy != "gone-user" || got.CreatorUsername != "" || got.CreatorEmail != "" {
		t.Errorf("creator = found %v, id %q, %q %q; want the id alone", got.CreatorFound, got.Key.CreatedBy, got.CreatorUsername, got.CreatorEmail)
	}
}

func TestNSSKeyAdminList_ShouldPageByNameWhenTheListIsLong(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	for _, name := range []string{"c", "a", "b"} {
		createAdminKey(t, keys, nssKeyAdmin, name, "", 0)
	}

	first, err := keys.AdminList(t.Context(), NSSAdminKeyFilter{Page: paging.Params{Limit: 2}})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Keys) != 2 || first.Keys[0].Key.Name != "a" || first.Keys[1].Key.Name != "b" || first.Total != 3 || first.NextCursor == nil {
		t.Fatalf("first page = %+v, total %d, next %v; want a, b of 3 and a cursor", first.Keys, first.Total, first.NextCursor)
	}
	second, err := keys.AdminList(t.Context(), NSSAdminKeyFilter{Page: paging.Params{Limit: 2, After: *first.NextCursor}})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Keys) != 1 || second.Keys[0].Key.Name != "c" || second.Total != 3 || second.NextCursor != nil {
		t.Errorf("second page = %+v, total %d, next %v; want c of 3 and no cursor", second.Keys, second.Total, second.NextCursor)
	}

	_, err = keys.AdminList(t.Context(), NSSAdminKeyFilter{Page: paging.Params{Limit: 2, After: "nss_nothere"}})
	var invalid *errorresponses.InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Errorf("unknown cursor = %v, want a 400", err)
	}
}

func TestNSSKeyAdminList_ShouldListNothingWhenThereAreNoKeys(t *testing.T) {
	t.Parallel()

	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), testdb.New(t))
	page, err := keys.AdminList(t.Context(), NSSAdminKeyFilter{Page: paging.Params{Limit: 25, Query: "10.0.4.7"}})
	if err != nil {
		t.Fatalf("AdminList: %v", err)
	}
	if len(page.Keys) != 0 || page.Total != 0 || page.Address == nil || page.Address.Display != "10.0.4.7" {
		t.Errorf("page = %+v, want no keys and the address read", page)
	}
}

func TestNSSKeyAdminList_ShouldReturnTheDatabaseErrorWhenATableCannotBeRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		table string
		q     string
	}{
		{name: "should fail when the usage cannot be read", table: "nss_key_usage", q: ""},
		{name: "should fail when the alerts cannot be read", table: "nss_alerts", q: ""},
		{name: "should fail when the usage cannot be searched", table: "nss_key_usage", q: "10.0.4.7"},
		{name: "should fail when the keys cannot be read", table: "nss_keys", q: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
			createAdminKey(t, keys, nssKeyAdmin, "web fleet", "web-admins", 0)
			drop := "DROP TABLE " + tt.table
			if testdb.IsPostgres(t) {
				// Postgres refuses to drop a table others reference
				// unless told to take their foreign keys with it.
				drop += " CASCADE"
			}
			if err := db.Exec(drop).Error; err != nil {
				t.Fatalf("drop %s: %v", tt.table, err)
			}
			if page, err := keys.AdminList(t.Context(), withQuery(tt.q)); err == nil {
				t.Errorf("AdminList = %+v, want the error reading %s", page, tt.table)
			}
		})
	}
}

func TestSecretGeneration_ShouldNameTheSecretRelativeToTheKeyWhenAsked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		presented, current int
		want               model.NSSSecretGeneration
	}{
		{name: "should say current when the host presented the current generation", presented: 3, current: 3, want: model.NSSSecretGenerationCurrent},
		{name: "should say previous when the host presented the one before", presented: 2, current: 3, want: model.NSSSecretGenerationPrevious},
		{name: "should say older when the host presented one further back", presented: 1, current: 3, want: model.NSSSecretGenerationOlder},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := secretGeneration(tt.presented, tt.current); got != tt.want {
				t.Errorf("secretGeneration(%d, %d) = %s, want %s", tt.presented, tt.current, got, tt.want)
			}
		})
	}
}

func TestNSSKeyAdminList_ShouldUseTheDefaultPageSizeWhenNoneIsGiven(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	for i := range paging.DefaultLimit + 1 {
		createAdminKey(t, keys, nssKeyAdmin, fmt.Sprintf("key %02d", i), "", 0)
	}

	page, err := keys.AdminList(t.Context(), NSSAdminKeyFilter{})
	if err != nil {
		t.Fatalf("AdminList: %v", err)
	}
	if len(page.Keys) != paging.DefaultLimit || page.NextCursor == nil {
		t.Errorf("keys = %d, next %v; want a page of %d and a cursor", len(page.Keys), page.NextCursor, paging.DefaultLimit)
	}
}
