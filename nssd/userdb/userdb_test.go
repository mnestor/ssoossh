package userdb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/varlink"
	"github.com/mnestor/ssoossh/nssd/cache"
	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/upstream"
)

func TestGetUserRecordKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  map[string]any
		wantErr string
		wantUID float64
	}{
		{name: "should find by name when only userName is given", params: p("userName", "alice"), wantUID: 5001},
		{name: "should find by uid when only uid is given", params: p("uid", 5001), wantUID: 5001},
		{name: "should find when uid and userName name the same user", params: p("uid", 5001, "userName", "alice"), wantUID: 5001},
		{name: "should report a conflict when uid and userName name different users", params: p("uid", 5003, "userName", "alice"), wantErr: ErrorConflictingRecordFound},
		{name: "should refuse to enumerate when neither key is given", params: p(), wantErr: ErrorEnumerationNotSupported},
		{name: "should not find when the server has no such name", params: p("userName", "nobody"), wantErr: ErrorNoRecordFound},
		{name: "should not find when the server has no such uid", params: p("uid", 9999), wantErr: ErrorNoRecordFound},
		{name: "should answer BadService when service is missing", params: map[string]any{"userName": "alice"}, wantErr: ErrorBadService},
		{name: "should answer BadService when service is someone else's", params: map[string]any{"userName": "alice", "service": "io.systemd.Home"}, wantErr: ErrorBadService},
		{name: "should refuse fuzzyNames when fuzzy matching is not supported", params: p("fuzzyNames", []string{"ali"}), wantErr: varlink.ErrorInvalidParameter},
		{name: "should refuse uuid when uuids are not supported", params: p("uuid", "0f3ab"), wantErr: varlink.ErrorInvalidParameter},
		{name: "should accept a null fuzzyNames when it is absent in effect", params: p("userName", "alice", "fuzzyNames", nil), wantUID: 5001},
		{name: "should refuse a uid of the wrong type when it is a string", params: p("uid", "5001"), wantErr: varlink.ErrorInvalidParameter},
		{name: "should refuse a negative uid when it is not a uid", params: p("uid", -1), wantErr: varlink.ErrorInvalidParameter},
		{name: "should refuse UID_INVALID when it is not a uid", params: p("uid", 4294967295), wantErr: varlink.ErrorInvalidParameter},
		{name: "should not match when dispositionMask excludes regular", params: p("userName", "alice", "dispositionMask", []string{"system"}), wantErr: ErrorNonMatchingRecordFound},
		{name: "should match when dispositionMask includes regular", params: p("userName", "alice", "dispositionMask", []string{"system", "regular"}), wantUID: 5001},
		{name: "should not match when uidMin is above the uid", params: p("userName", "alice", "uidMin", 6000), wantErr: ErrorNonMatchingRecordFound},
		{name: "should not match when uidMax is below the uid", params: p("userName", "alice", "uidMax", 5000), wantErr: ErrorNonMatchingRecordFound},
		{name: "should match when the uid is within uidMin and uidMax", params: p("userName", "alice", "uidMin", 5001, "uidMax", 5001), wantUID: 5001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			r := one(t, f.call(t, MethodGetUserRecord, tt.params, userPeer, false))
			if tt.wantErr != "" {
				if r.Error != tt.wantErr {
					t.Fatalf("got %q %s, want %q", r.Error, r.Parameters, tt.wantErr)
				}
				return
			}
			rec, _ := decodeRecord(t, r)
			if rec["uid"] != tt.wantUID {
				t.Fatalf("got %v", rec)
			}
		})
	}
}

func TestGetUserRecordShouldNameTheParameterWhenItIsInvalid(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	r := one(t, f.call(t, MethodGetUserRecord, p("fuzzyNames", []string{"a"}), userPeer, false))
	if string(r.Parameters) != `{"parameter":"fuzzyNames"}` {
		t.Fatalf("got %s", r.Parameters)
	}
	r = one(t, f.call(t, MethodGetUserRecord, p("uid", "x"), userPeer, false))
	if string(r.Parameters) != `{"parameter":"uid"}` {
		t.Fatalf("got %s", r.Parameters)
	}
}

func TestUserRecordMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		user       string
		peer       Peer
		wantShell  string
		wantLocked bool
	}{
		{name: "should map an active user when it is served to a uid 0 peer", user: "alice", peer: rootPeer, wantShell: "/bin/bash"},
		{name: "should map an active user when it is served to anyone else", user: "alice", peer: userPeer, wantShell: "/bin/bash"},
		{name: "should lock a disabled user with the disabled shell when it is served", user: "carol", peer: rootPeer, wantShell: "/usr/sbin/nologin", wantLocked: true},
		{name: "should lock a deleted user with the disabled shell when it is served", user: "dave", peer: userPeer, wantShell: "/usr/sbin/nologin", wantLocked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			rec, incomplete := decodeRecord(t, one(t, f.call(t, MethodGetUserRecord, p("userName", tt.user), tt.peer, false)))
			if rec["userName"] != tt.user || rec["shell"] != tt.wantShell || rec["disposition"] != "regular" ||
				rec["service"] != ServiceName || rec["homeDirectory"] != "/home/"+tt.user || rec["gid"] != rec["uid"] {
				t.Fatalf("got %v", rec)
			}
			if rec["realName"] != "User "+tt.user {
				t.Fatalf("should sanitise realName when it holds ':', got %q", rec["realName"])
			}
			if locked, _ := rec["locked"].(bool); locked != tt.wantLocked {
				t.Fatalf("got locked %v, want %v", rec["locked"], tt.wantLocked)
			}
			if _, has := rec["locked"]; !tt.wantLocked && has {
				t.Fatal("should omit locked when the user is active")
			}
			if _, has := rec["memberOf"]; has {
				t.Fatal("should never send memberOf")
			}
			priv, hasPriv := rec["privileged"].(map[string]any)
			if tt.peer.UID == 0 {
				if !hasPriv || incomplete {
					t.Fatalf("should send privileged and be complete when the peer is uid 0, got %v incomplete=%v", rec, incomplete)
				}
				if h, _ := priv["hashedPassword"].([]any); len(h) != 1 || h[0] != "*" {
					t.Fatalf("got privileged %v", priv)
				}
			} else if hasPriv || !incomplete {
				t.Fatalf("should omit privileged and mark incomplete when the peer is not uid 0, got %v incomplete=%v", rec, incomplete)
			}
		})
	}
}

func TestLocalShortCircuitShouldNeverAskUpstreamWhenTheNameIsLocal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		params map[string]any
	}{
		{name: "should not look up a local user name", method: MethodGetUserRecord, params: p("userName", "bob")},
		{name: "should not look up a local uid", method: MethodGetUserRecord, params: p("uid", 1001)},
		{name: "should not look up a uid below min_id", method: MethodGetUserRecord, params: p("uid", 0)},
		{name: "should not look up a filtered name", method: MethodGetUserRecord, params: p("userName", "root")},
		{name: "should not look up an invalid name", method: MethodGetUserRecord, params: p("userName", "a:b")},
		{name: "should not look up an over-long name", method: MethodGetUserRecord, params: p("userName", strings.Repeat("a", 33))},
		{name: "should not look up a local group name", method: MethodGetGroupRecord, params: p("groupName", "sudo")},
		{name: "should not look up a gid below min_id", method: MethodGetGroupRecord, params: p("gid", 27)},
		{name: "should not look up a local user's memberships", method: MethodGetMemberships, params: p("userName", "bob")},
		{name: "should not look up a local group's members", method: MethodGetMemberships, params: p("groupName", "sudo")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			if got := errorOf(t, f.call(t, tt.method, tt.params, rootPeer, true)); got != ErrorNoRecordFound {
				t.Fatalf("got %q", got)
			}
			if n := f.up.callCount(); n != 0 {
				t.Fatalf("made %d upstream calls", n)
			}
		})
	}
}

func TestGetGroupRecordKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  map[string]any
		wantErr string
	}{
		{name: "should find by name when only groupName is given", params: p("groupName", "devs")},
		{name: "should find by gid when only gid is given", params: p("gid", 5100)},
		{name: "should find when gid and groupName name the same group", params: p("gid", 5100, "groupName", "devs")},
		{name: "should report a conflict when gid and groupName differ", params: p("gid", 5101, "groupName", "devs"), wantErr: ErrorConflictingRecordFound},
		{name: "should refuse to enumerate when neither key is given", params: p(), wantErr: ErrorEnumerationNotSupported},
		{name: "should not find when the server has no such group", params: p("groupName", "nogroup"), wantErr: ErrorNoRecordFound},
		{name: "should answer BadService when service is missing", params: map[string]any{"groupName": "devs"}, wantErr: ErrorBadService},
		{name: "should refuse fuzzyNames when fuzzy matching is not supported", params: p("fuzzyNames", []string{"d"}), wantErr: varlink.ErrorInvalidParameter},
		{name: "should refuse uuid when uuids are not supported", params: p("uuid", "x"), wantErr: varlink.ErrorInvalidParameter},
		{name: "should refuse a gid of the wrong type when it is a string", params: p("gid", "5100"), wantErr: varlink.ErrorInvalidParameter},
		{name: "should refuse a negative gid when it is not a gid", params: p("gid", -5), wantErr: varlink.ErrorInvalidParameter},
		{name: "should not match when gidMin is above the gid", params: p("groupName", "devs", "gidMin", 6000), wantErr: ErrorNonMatchingRecordFound},
		{name: "should not match when gidMax is below the gid", params: p("groupName", "devs", "gidMax", 10), wantErr: ErrorNonMatchingRecordFound},
		{name: "should not match when dispositionMask excludes regular", params: p("groupName", "devs", "dispositionMask", []string{"intrinsic"}), wantErr: ErrorNonMatchingRecordFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			r := one(t, f.call(t, MethodGetGroupRecord, tt.params, userPeer, false))
			if tt.wantErr != "" {
				if r.Error != tt.wantErr {
					t.Fatalf("got %q, want %q", r.Error, tt.wantErr)
				}
				return
			}
			rec, incomplete := decodeRecord(t, r)
			if rec["groupName"] != "devs" || rec["gid"] != float64(5100) || rec["disposition"] != "regular" || rec["service"] != ServiceName || incomplete {
				t.Fatalf("got %v incomplete=%v", rec, incomplete)
			}
			if _, has := rec["members"]; has {
				t.Fatal("should never send members in the group record")
			}
		})
	}
}

func TestGetGroupRecordShouldAnswerFromAUserLookupWhenTheUserFilledTheGroup(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.call(t, MethodGetUserRecord, p("userName", "alice"), rootPeer, false)
	before := f.up.callCount()
	// initgroups: the user's own private group and devs come from the
	// user answer, by name and by gid.
	for _, params := range []map[string]any{p("groupName", "devs"), p("gid", 5001), p("groupName", "alice")} {
		if r := one(t, f.call(t, MethodGetGroupRecord, params, rootPeer, false)); r.Error != "" {
			t.Fatalf("%v: got %s", params, r.Error)
		}
	}
	if n := f.up.callCount() - before; n != 0 {
		t.Fatalf("made %d upstream calls for groups a user answer filled", n)
	}
}

func TestGetMemberships(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  map[string]any
		more    bool
		want    []string
		wantErr string
	}{
		{name: "should list the user's groups when userName is given", params: p("userName", "alice"), more: true, want: []string{"alice/alice", "alice/devs", "alice/staff"}},
		{name: "should list the group's members when groupName is given", params: p("groupName", "devs"), more: true, want: []string{"alice/devs", "carol/devs"}},
		{name: "should answer one pair when both name a membership", params: p("userName", "alice", "groupName", "devs"), want: []string{"alice/devs"}},
		{name: "should not find when both name no membership", params: p("userName", "alice", "groupName", "ops"), wantErr: ErrorNoRecordFound},
		{name: "should answer a single member without more when there is one", params: p("groupName", "solo"), want: []string{"alice/solo"}},
		{name: "should ask for more when a stream is needed and more is not set", params: p("groupName", "devs"), wantErr: varlink.ErrorExpectedMore},
		{name: "should not find when the group has no members", params: p("groupName", "none"), more: true, wantErr: ErrorNoRecordFound},
		{name: "should not find when the user does not exist", params: p("userName", "nobody"), more: true, wantErr: ErrorNoRecordFound},
		{name: "should not find when the group does not exist", params: p("groupName", "nogroup"), more: true, wantErr: ErrorNoRecordFound},
		{name: "should refuse to enumerate when neither key is given", params: p(), more: true, wantErr: ErrorEnumerationNotSupported},
		{name: "should answer BadService when service is wrong", params: map[string]any{"userName": "alice", "service": "x"}, wantErr: ErrorBadService},
		{name: "should refuse a name of the wrong type when it is a number", params: p("userName", 5), wantErr: varlink.ErrorInvalidParameter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			replies := f.call(t, MethodGetMemberships, tt.params, rootPeer, tt.more)
			if tt.wantErr != "" {
				if got := errorOf(t, replies); got != tt.wantErr {
					t.Fatalf("got %q, want %q", got, tt.wantErr)
				}
				return
			}
			var got []string
			for i, r := range replies {
				var m membershipReply
				if err := json.Unmarshal(r.Parameters, &m); err != nil || r.Error != "" {
					t.Fatalf("reply %d: %+v, %v", i, r, err)
				}
				if r.Continues != (i < len(replies)-1) {
					t.Fatalf("reply %d: continues %v", i, r.Continues)
				}
				got = append(got, m.UserName+"/"+m.GroupName)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetMembershipsByGroupShouldFetchMembersWhenOnlyAStubIsCached(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.call(t, MethodGetUserRecord, p("userName", "alice"), rootPeer, false)
	before := f.up.callCount()
	replies := f.call(t, MethodGetMemberships, p("groupName", "devs"), rootPeer, true)
	if len(replies) != 2 || f.up.callCount() != before+1 {
		t.Fatalf("got %d replies after %d calls", len(replies), f.up.callCount()-before)
	}
	// Now cached with its members.
	f.call(t, MethodGetMemberships, p("groupName", "devs"), rootPeer, true)
	if f.up.callCount() != before+1 {
		t.Fatal("should answer the second time from the cache")
	}
}

func TestGetMembershipsShouldDropMembersWhenIgnoreGroupMembersIsSet(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *config.Config) { c.IgnoreGroupMembers = true })
	if got := errorOf(t, f.call(t, MethodGetMemberships, p("groupName", "devs"), rootPeer, true)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
}

func TestCacheFreshness(t *testing.T) {
	t.Parallel()
	ttl := 5400 * time.Second

	tests := []struct {
		name      string
		advance   time.Duration
		wantCalls int
		wantQueue int
	}{
		{name: "should answer from the cache when the entry is fresh", advance: ttl/2 - time.Second, wantCalls: 1},
		{name: "should answer from the cache and queue a refresh when past nowait", advance: ttl / 2, wantCalls: 1, wantQueue: 1},
		{name: "should fetch again when the entry has expired", advance: ttl, wantCalls: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
			f.clock.Advance(tt.advance)
			if r := one(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); r.Error != "" {
				t.Fatalf("got %s", r.Error)
			}
			if n := f.up.callCount(); n != tt.wantCalls {
				t.Fatalf("made %d upstream calls, want %d", n, tt.wantCalls)
			}
			if n := f.d.refresher.Pending(); n != tt.wantQueue {
				t.Fatalf("queued %d refreshes, want %d", n, tt.wantQueue)
			}
		})
	}
}

func TestNegativeCacheShouldAnswerMissesLocallyWhenWithinTheTimeout(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	for range 3 {
		if got := errorOf(t, f.call(t, MethodGetUserRecord, p("userName", "nobody"), userPeer, false)); got != ErrorNoRecordFound {
			t.Fatalf("got %q", got)
		}
	}
	if n := f.up.callCount(); n != 1 {
		t.Fatalf("made %d upstream calls", n)
	}
	f.clock.Advance(15 * time.Second)
	f.call(t, MethodGetUserRecord, p("userName", "nobody"), userPeer, false)
	if n := f.up.callCount(); n != 2 {
		t.Fatalf("should ask again after entry_negative_timeout, made %d calls", n)
	}
}

func TestRemovedUserShouldLeaveTheCacheWhenTheServerSaysNotFound(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	delete(f.up.users, "alice")
	f.clock.Advance(2 * time.Hour)
	if got := errorOf(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
	// Later offline, the removed user must not come back from the cache.
	f.clock.Advance(time.Minute)
	f.up.setErr(upstream.ErrOffline)
	if got := errorOf(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); got != ErrorServiceNotAvailable {
		t.Fatalf("got %q", got)
	}
	// The same through a uid lookup.
	f.up.setErr(nil)
	f.call(t, MethodGetUserRecord, p("userName", "carol"), userPeer, false)
	delete(f.up.users, "carol")
	f.clock.Advance(2 * time.Hour)
	if got := errorOf(t, f.call(t, MethodGetUserRecord, p("uid", 5003), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
	if e, _ := f.store.UserByName(t.Context(), "carol"); e != nil {
		t.Fatal("should drop the entry a uid miss names")
	}
}

func TestOfflineServing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		expireOffline time.Duration
		advance       time.Duration
		cached        bool
		err           error
		want          string
	}{
		{name: "should serve an expired entry when the server is offline", advance: 3 * time.Hour, cached: true, err: upstream.ErrOffline, want: ""},
		{name: "should serve an expired entry when the key is latched", advance: 3 * time.Hour, cached: true, err: upstream.ErrUnauthorized, want: ""},
		{name: "should serve an expired entry when over the concurrency cap", advance: 3 * time.Hour, cached: true, err: upstream.ErrBusy, want: ""},
		{name: "should answer ServiceNotAvailable when offline with no entry", err: upstream.ErrOffline, want: ErrorServiceNotAvailable},
		{name: "should answer ServiceNotAvailable when latched with no entry", err: upstream.ErrUnauthorized, want: ErrorServiceNotAvailable},
		{name: "should serve within cache_expire_offline when it is set", expireOffline: 24 * time.Hour, advance: 23 * time.Hour, cached: true, err: upstream.ErrOffline, want: ""},
		{name: "should stop serving past cache_expire_offline when it is set", expireOffline: 24 * time.Hour, advance: 24 * time.Hour, cached: true, err: upstream.ErrOffline, want: ErrorServiceNotAvailable},
		{name: "should not cache an invalid answer when the server refuses the name", err: upstream.ErrInvalid, want: ErrorNoRecordFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(c *config.Config) { c.CacheExpireOffline = tt.expireOffline })
			if tt.cached {
				f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
			}
			f.clock.Advance(tt.advance)
			f.up.setErr(tt.err)
			if got := one(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)).Error; got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if errors.Is(tt.err, upstream.ErrInvalid) && f.d.negative.Len() != 0 {
				t.Fatal("should not negative-cache an invalid answer")
			}
		})
	}
}

func TestStaleFlagShouldForceAFetchWhenTheEntryIsMarked(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	n, err := f.d.Invalidate(t.Context(), cache.Selector{Users: true, Name: "alice"})
	if err != nil || n != 1 {
		t.Fatalf("got %d, %v", n, err)
	}
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	if f.up.callCount() != 2 {
		t.Fatalf("should refetch a stale entry, made %d calls", f.up.callCount())
	}
}

func TestSlowUpstreamShouldAnswerServiceNotAvailableWhenTheDeadlinePasses(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.up.block = make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	call := varlink.Call{Method: MethodGetUserRecord, Parameters: mustJSON(t, p("userName", "alice"))}
	if got := errorOf(t, f.callCtx(t, ctx, call, userPeer)); got != ErrorServiceNotAvailable {
		t.Fatalf("got %q", got)
	}
	// The fetch completes into the cache after the caller has gone.
	close(f.up.block)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if e, _ := f.store.UserByName(t.Context(), "alice"); e != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fetch never completed into the cache")
		}
		time.Sleep(time.Millisecond)
	}
	if r := one(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); r.Error != "" || f.up.callCount() != 1 {
		t.Fatalf("should answer the next call from the cache, got %q after %d calls", r.Error, f.up.callCount())
	}
}

func TestSSHDFreshness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		peer      Peer
		advance   time.Duration
		upErr     error
		remove    bool
		wantCalls int
		wantErr   string
	}{
		{name: "should refresh when sshd asks and the entry is older than login_soft_ttl", peer: sshdPeer, advance: 61 * time.Second, wantCalls: 2},
		{name: "should not refresh when sshd asks and the entry is younger than login_soft_ttl", peer: sshdPeer, advance: 59 * time.Second, wantCalls: 1},
		{name: "should not refresh when a uid 0 peer is not sshd", peer: rootPeer, advance: 61 * time.Second, wantCalls: 1},
		{name: "should answer from the cache when the login refresh cannot reach the server", peer: sshdPeer, advance: 61 * time.Second, upErr: upstream.ErrOffline, wantCalls: 2},
		{name: "should answer not found when the login refresh says the user is gone", peer: sshdPeer, advance: 61 * time.Second, remove: true, wantCalls: 2, wantErr: ErrorNoRecordFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
			f.clock.Advance(tt.advance)
			f.up.setErr(tt.upErr)
			if tt.remove {
				delete(f.up.users, "alice")
			}
			if got := one(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), tt.peer, false)).Error; got != tt.wantErr {
				t.Fatalf("got %q, want %q", got, tt.wantErr)
			}
			if n := f.up.callCount(); n != tt.wantCalls {
				t.Fatalf("made %d upstream calls, want %d", n, tt.wantCalls)
			}
		})
	}
}

func TestSSHDFreshnessShouldAnswerFromTheCacheWhenTheRefreshIsSlow(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *config.Config) { c.LoginRefreshTimeout = 20 * time.Millisecond })
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	f.clock.Advance(2 * time.Minute)
	f.up.block = make(chan struct{})
	defer close(f.up.block)
	start := time.Now()
	if r := one(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), sshdPeer, false)); r.Error != "" {
		t.Fatalf("got %q", r.Error)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("should give up on the refresh at login_refresh_timeout")
	}
}

func TestDisabledUserServedToSSHDShouldBeLoggedWhenItHappens(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.call(t, MethodGetUserRecord, p("userName", "carol"), sshdPeer, false)
	if !strings.Contains(f.logs.String(), "serving a disabled account to sshd") {
		t.Fatalf("got logs %s", f.logs)
	}
}

func TestRefreshChangingIDsShouldBeLoggedWhenTheServerMovesAUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	f.up.users["alice"].UID = 6001
	f.up.users["alice"].GID = 6001
	f.up.groups["devs"].GID = 6100
	f.clock.Advance(2 * time.Hour)
	rec, _ := decodeRecord(t, one(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)))
	if rec["uid"] != float64(6001) {
		t.Fatalf("should apply the change, got %v", rec)
	}
	logs := f.logs.String()
	if !strings.Contains(logs, "a refresh changed a cached user's ids") || !strings.Contains(logs, "old_uid=5001") || !strings.Contains(logs, "uid=6001") {
		t.Fatalf("got logs %s", logs)
	}
	f.call(t, MethodGetMemberships, p("groupName", "devs"), userPeer, true)
	if !strings.Contains(f.logs.String(), "a refresh changed a cached group's gid") {
		t.Fatalf("should log a group's gid change too, got %s", f.logs)
	}
}

func TestPolicyShouldApplyOnReadWhenOverridesChange(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	if err := os.WriteFile(f.paths.Overrides, []byte("alice:\n  home: /srv/alice\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.d.ReloadPolicy()
	rec, _ := decodeRecord(t, one(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)))
	if rec["homeDirectory"] != "/srv/alice" || f.up.callCount() != 1 {
		t.Fatalf("should apply an override to a cached entry without a fetch, got %v after %d calls", rec, f.up.callCount())
	}
	// An override that breaks the sanity rules hides the cached user.
	if err := os.WriteFile(f.paths.Overrides, []byte("alice:\n  uid: 10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.d.ReloadPolicy()
	if got := errorOf(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
}

func TestPolicyShouldRefuseAndNegativeCacheWhenTheServerAnswerFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *config.Config) { c.RequireGroups = []string{"admins"} })
	for range 2 {
		if got := errorOf(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); got != ErrorNoRecordFound {
			t.Fatalf("got %q", got)
		}
	}
	if f.up.callCount() != 1 || !strings.Contains(f.logs.String(), "refusing a user from the server") {
		t.Fatalf("made %d calls; logs %s", f.up.callCount(), f.logs)
	}
}

func TestMismatchedAnswerShouldBeRefusedWhenTheServerSendsAnotherRecord(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.up.users["mallory"] = userResp("alice", 5001, apitypes.NSSStatusActive)
	if got := errorOf(t, f.call(t, MethodGetUserRecord, p("userName", "mallory"), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
	f.up.groups["evil"] = &apitypes.NSSGroupResponse{Name: "devs", GID: 5100}
	if got := errorOf(t, f.call(t, MethodGetGroupRecord, p("groupName", "evil"), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
}

func TestGroupResolutionFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.up.groups["low"] = &apitypes.NSSGroupResponse{Name: "low2", GID: 5200}
	f.up.groups["low2"] = &apitypes.NSSGroupResponse{Name: "low2", GID: 5200, Members: []string{"bob"}}
	// bob is local, so the only member is dropped.
	if got := errorOf(t, f.call(t, MethodGetMemberships, p("groupName", "low2"), userPeer, true)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
	f.up.setErr(upstream.ErrInvalid)
	if got := errorOf(t, f.call(t, MethodGetGroupRecord, p("groupName", "fresh"), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
	f.up.setErr(upstream.ErrOffline)
	if got := errorOf(t, f.call(t, MethodGetGroupRecord, p("groupName", "fresh2"), userPeer, false)); got != ErrorServiceNotAvailable {
		t.Fatalf("got %q", got)
	}
	// An expired group with members is served offline.
	f.up.setErr(nil)
	f.call(t, MethodGetMemberships, p("groupName", "devs"), userPeer, true)
	f.clock.Advance(3 * time.Hour)
	f.up.setErr(upstream.ErrOffline)
	if replies := f.call(t, MethodGetMemberships, p("groupName", "devs"), userPeer, true); len(replies) != 2 {
		t.Fatalf("got %+v", replies)
	}
	// A removed group leaves the cache.
	f.up.setErr(nil)
	delete(f.up.groups, "devs")
	if got := errorOf(t, f.call(t, MethodGetGroupRecord, p("gid", 5100), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
	// A group the policy refuses is negative-cached.
	f.up.groups["sysgrp"] = &apitypes.NSSGroupResponse{Name: "sysgrp", GID: 5300}
	if err := os.WriteFile(f.paths.Group, []byte("sysgrp:x:5300:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := errorOf(t, f.call(t, MethodGetGroupRecord, p("gid", 5300), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
}

func TestGroupFreshnessShouldQueueARefreshWhenPastNowait(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.call(t, MethodGetMemberships, p("groupName", "devs"), userPeer, true)
	f.clock.Advance(46 * time.Minute)
	f.call(t, MethodGetGroupRecord, p("groupName", "devs"), userPeer, false)
	if f.d.refresher.Pending() != 1 {
		t.Fatalf("queued %d", f.d.refresher.Pending())
	}
	// A cached group the policy now refuses is not served.
	if err := os.WriteFile(f.paths.Group, []byte("devs:x:5100:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.d.ReloadPolicy()
	if got := errorOf(t, f.call(t, MethodGetGroupRecord, p("gid", 5100), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
}

func TestRefreshUser(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)

	if r := one(t, f.call(t, MethodRefreshUser, p("userName", "alice"), userPeer, false)); r.Error != "" || string(r.Parameters) != "{}" {
		t.Fatalf("should reply at once with nothing, got %+v", r)
	}
	if f.d.refresher.Pending() != 1 || f.up.callCount() != 0 {
		t.Fatal("should queue a background refresh, not fetch inline")
	}
	// Debounced per name for login_soft_ttl.
	f.call(t, MethodRefreshUser, p("userName", "alice"), userPeer, false)
	f.clock.Advance(59 * time.Second)
	f.call(t, MethodRefreshUser, map[string]any{"userName": "alice"}, userPeer, false)
	if f.d.refresher.Pending() != 1 {
		t.Fatalf("should debounce within login_soft_ttl, queued %d", f.d.refresher.Pending())
	}
	// Oneway: acted on, never answered.
	f.clock.Advance(2 * time.Second)
	if replies := f.callCtx(t, t.Context(), varlink.Call{Method: MethodRefreshUser, Parameters: mustJSON(t, p("userName", "carol")), Oneway: true}, userPeer); len(replies) != 0 {
		t.Fatalf("should not answer a oneway call, got %+v", replies)
	}
	if f.d.refresher.Pending() != 2 {
		t.Fatalf("queued %d", f.d.refresher.Pending())
	}
	// Refused names are ignored quietly.
	f.call(t, MethodRefreshUser, p("userName", "bob"), userPeer, false)
	if f.d.refresher.Pending() != 2 {
		t.Fatal("should ignore a local name")
	}
	if got := errorOf(t, f.call(t, MethodRefreshUser, map[string]any{"userName": "a", "service": "x"}, userPeer, false)); got != ErrorBadService {
		t.Fatalf("got %q", got)
	}
	if got := errorOf(t, f.call(t, MethodRefreshUser, map[string]any{}, userPeer, false)); got != varlink.ErrorInvalidParameter {
		t.Fatalf("got %q", got)
	}
	if got := errorOf(t, f.call(t, MethodRefreshUser, map[string]any{"userName": 1}, userPeer, false)); got != varlink.ErrorInvalidParameter {
		t.Fatalf("got %q", got)
	}
}

func TestRefreshUserDebounceShouldStayBoundedWhenFlooded(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	for i := range maxDebounce {
		f.d.debounce["u"+time.Duration(i).String()] = f.clock.Now()
	}
	if f.d.takeDebounce("newname") {
		t.Fatal("should drop a request when every debounce entry is recent")
	}
	f.clock.Advance(time.Hour)
	if !f.d.takeDebounce("newname") || len(f.d.debounce) != 1 {
		t.Fatalf("should prune aged entries, have %d", len(f.d.debounce))
	}
}

func TestBackgroundWork(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(c *config.Config) { c.RefreshRate = 1000; c.RefreshExpiredInterval = time.Hour })
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	f.call(t, MethodGetMemberships, p("groupName", "devs"), userPeer, true)
	f.clock.Advance(50 * time.Minute)

	// One sweep pass queues the user and the member-bearing group, not
	// the stubs the user filled.
	f.d.sweep(t.Context())
	if f.d.refresher.Pending() != 2 {
		t.Fatalf("queued %d", f.d.refresher.Pending())
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { f.d.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.up.callCount() < 4 {
		if time.Now().After(deadline) {
			t.Fatalf("background refreshes did not run, %d calls", f.up.callCount())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

func TestRefreshShouldIgnoreUnknownKeysWhenOneIsQueued(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.d.refresh(t.Context(), "nonsense")
	f.up.setErr(upstream.ErrOffline)
	f.d.refresh(t.Context(), "user/uid/5001")
	f.d.refresh(t.Context(), "group/gid/5100")
	if !strings.Contains(f.logs.String(), "background refresh failed") {
		t.Fatalf("got logs %s", f.logs)
	}
}

func TestSweepShouldLogWhenTheStoreFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.d.store = failingStore{}
	f.d.sweep(t.Context())
	if !strings.Contains(f.logs.String(), "cannot list the user cache") || !strings.Contains(f.logs.String(), "cannot list the group cache") {
		t.Fatalf("got logs %s", f.logs)
	}
}

func TestGetInfo(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	var got infoReply
	if err := json.Unmarshal(one(t, f.call(t, MethodGetInfo, nil, userPeer, false)).Parameters, &got); err != nil {
		t.Fatal(err)
	}
	if got.Product != "ssoossh-nss" || len(got.Interfaces) != 3 || got.Interfaces[1] != InterfaceUserDatabase {
		t.Fatalf("got %+v", got)
	}
}

func TestUnknownMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		want   string
		params string
	}{
		{name: "should answer MethodNotFound when the interface is known", method: InterfaceUserDatabase + ".Frobnicate", want: varlink.ErrorMethodNotFound, params: `{"method":"io.systemd.UserDatabase.Frobnicate"}`},
		{name: "should answer MethodNotFound for introspection when it is not implemented", method: InterfaceVarlink + ".GetInterfaceDescription", want: varlink.ErrorMethodNotFound},
		{name: "should answer InterfaceNotFound when the interface is unknown", method: "io.systemd.Hostname.Describe", want: varlink.ErrorInterfaceNotFound, params: `{"interface":"io.systemd.Hostname"}`},
		{name: "should answer InterfaceNotFound when the method has no interface", method: "Describe", want: varlink.ErrorInterfaceNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			r := one(t, f.call(t, tt.method, nil, userPeer, false))
			if r.Error != tt.want || (tt.params != "" && string(r.Parameters) != tt.params) {
				t.Fatalf("got %q %s", r.Error, r.Parameters)
			}
		})
	}
}

func TestAdmin(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ctx := t.Context()
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	f.call(t, MethodGetUserRecord, p("userName", "nobody"), userPeer, false)

	s := f.d.Status(ctx)
	if s.Cache.Users != 1 || s.Cache.Groups != 2 || s.NegativeEntries != 1 || s.Upstream.State != upstream.StateOnline || s.CacheMode != config.CacheModePersistent {
		t.Fatalf("got %+v", s)
	}

	tests := []struct {
		name string
		sel  cache.Selector
		want int
	}{
		{name: "should mark one user when named", sel: cache.Selector{Users: true, Name: "alice"}, want: 1},
		{name: "should mark one group when named", sel: cache.Selector{Groups: true, Name: "devs"}, want: 1},
		{name: "should mark everything when nothing is named", sel: cache.Selector{Users: true, Groups: true}, want: 3},
	}
	for _, tt := range tests {
		n, err := f.d.Invalidate(ctx, tt.sel)
		if err != nil || n != tt.want {
			t.Fatalf("%s: got %d, %v", tt.name, n, err)
		}
	}
	if f.d.negative.Len() != 0 {
		t.Fatal("should forget the misses when everything is invalidated")
	}

	f.d.Reset()
	if f.up.resets != 1 {
		t.Fatal("should reset the upstream on SIGHUP")
	}

	f.d.store = failingStore{}
	if s := f.d.Status(ctx); s.CacheError == "" {
		t.Fatal("should report a store that cannot count")
	}
	if _, err := f.d.Invalidate(ctx, cache.Selector{Users: true}); err == nil {
		t.Fatal("should report a store that cannot mark")
	}
}

func TestBrokenStoreShouldCostUpstreamCallsNotAnswersWhenItFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.d.store = failingStore{}
	if r := one(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); r.Error != "" {
		t.Fatalf("got %q", r.Error)
	}
	if r := one(t, f.call(t, MethodGetGroupRecord, p("groupName", "devs"), userPeer, false)); r.Error != "" {
		t.Fatalf("got %q", r.Error)
	}
	delete(f.up.users, "alice")
	delete(f.up.groups, "devs")
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	f.call(t, MethodGetGroupRecord, p("groupName", "devs"), userPeer, false)
	for _, want := range []string{"cannot read the user cache", "cannot write the user cache", "cannot write the group cache", "cannot read the group cache"} {
		if !strings.Contains(f.logs.String(), want) {
			t.Fatalf("should log %q, got %s", want, f.logs)
		}
	}
}

func TestCorruptEntryShouldNotBeServedWhenItDoesNotDecode(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	now := f.clock.Now()
	if err := f.store.PutUser(t.Context(), cache.UserEntry{Name: "alice", UID: 5001, GID: 5001, Data: []byte("{"), FetchedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.PutGroup(t.Context(), cache.GroupEntry{Name: "devs", GID: 5100, Members: true, Data: []byte("{"), FetchedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := errorOf(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); got != ErrorServiceNotAvailable {
		t.Fatalf("got %q", got)
	}
	if got := errorOf(t, f.call(t, MethodGetGroupRecord, p("groupName", "devs"), userPeer, false)); got != ErrorServiceNotAvailable {
		t.Fatalf("got %q", got)
	}
}

func TestKeysShouldRoundTripWhenParsed(t *testing.T) {
	t.Parallel()
	for _, k := range []userKey{{name: "alice"}, {uid: 5001, byUID: true}} {
		if got, ok := parseUserKey(k.String()); !ok || got != k {
			t.Fatalf("got %+v", got)
		}
	}
	for _, k := range []groupKey{{name: "devs"}, {gid: 5100, byGID: true}} {
		if got, ok := parseGroupKey(k.String()); !ok || got != k {
			t.Fatalf("got %+v", got)
		}
	}
	if _, ok := parseUserKey("user/uid/x"); ok {
		t.Fatal("should refuse a bad uid")
	}
	if _, ok := parseGroupKey("other"); ok {
		t.Fatal("should refuse another kind")
	}
}

// failingStore fails every call.
type failingStore struct{}

// errStore is failingStore's error.
var errStore = errors.New("disk on fire")

// The methods below implement cache.Store, every one of them failing.

func (failingStore) UserByName(context.Context, string) (*cache.UserEntry, error) {
	return nil, errStore
}
func (failingStore) UserByUID(context.Context, uint32) (*cache.UserEntry, error) {
	return nil, errStore
}
func (failingStore) PutUser(context.Context, cache.UserEntry) error { return errStore }
func (failingStore) DeleteUser(context.Context, string) error       { return errStore }
func (failingStore) GroupByName(context.Context, string) (*cache.GroupEntry, error) {
	return nil, errStore
}
func (failingStore) GroupByGID(context.Context, uint32) (*cache.GroupEntry, error) {
	return nil, errStore
}
func (failingStore) PutGroup(context.Context, cache.GroupEntry) error { return errStore }
func (failingStore) DeleteGroup(context.Context, string) error        { return errStore }
func (failingStore) MarkStale(context.Context, cache.Selector) (int, error) {
	return 0, errStore
}
func (failingStore) Users(context.Context) ([]cache.UserEntry, error)   { return nil, errStore }
func (failingStore) Groups(context.Context) ([]cache.GroupEntry, error) { return nil, errStore }
func (failingStore) Counts(context.Context) (cache.Counts, error)       { return cache.Counts{}, errStore }
func (failingStore) Close() error                                       { return nil }

func TestGroupNameCollision(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)

	// The member's list leaves out the colliding name, and by gid the
	// directory group is not served: files owned by it show the number.
	f.call(t, MethodGetUserRecord, p("userName", "alice"), rootPeer, false)
	if got := errorOf(t, f.call(t, MethodGetGroupRecord, p("gid", 2005), rootPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q, want the colliding gid not found", got)
	}
	s := f.d.Status(t.Context())
	if len(s.Groups.Collisions) != 1 || s.Groups.Collisions[0].Name != "users" || s.Groups.Collisions[0].GID != 2005 ||
		s.Groups.Collisions[0].LocalGID == nil || *s.Groups.Collisions[0].LocalGID != 100 {
		t.Fatalf("should list the collision in status, got %+v", s.Groups)
	}
	if len(s.Groups.Refused) != 1 || s.Groups.Refused[0].Name != "sudo" {
		t.Fatalf("should list sudo's system gid as refused in status, got %+v", s.Groups)
	}
	if n := strings.Count(f.logs.String(), "same name as a local group"); n != 1 {
		t.Fatalf("got %d collision warnings, want 1:\n%s", n, f.logs)
	}

	f.d.Reset()
	if s := f.d.Status(t.Context()); len(s.Groups.Collisions)+len(s.Groups.Refused) != 0 {
		t.Fatalf("should clear the lists on SIGHUP, got %+v", s.Groups)
	}
}

// ops is the group the coherence tests move users in and out of.
var ops = apitypes.NSSMembership{GID: 5103, Name: "ops"}

// putGroup caches resp at the fixture's now, as a group lookup would, or
// as a stub filled from a user's memberships when members is false.
func putGroup(t *testing.T, f *fixture, resp apitypes.NSSGroupResponse, members bool) {
	t.Helper()
	now := f.clock.Now()
	e := cache.GroupEntry{Name: resp.Name, GID: resp.GID, Members: members, FetchedAt: now, ExpiresAt: now.Add(f.cfg.TTLGroup())}
	if members {
		e.Data = mustJSON(t, resp)
	}
	if err := f.store.PutGroup(t.Context(), e); err != nil {
		t.Fatal(err)
	}
}

// putUser caches a user answer with groups at the fixture's now; stale
// makes the next lookup refetch it.
func putUser(t *testing.T, f *fixture, name string, uid uint32, groups []apitypes.NSSMembership, stale bool) {
	t.Helper()
	resp := userResp(name, uid, apitypes.NSSStatusActive)
	resp.Groups = groups
	now := f.clock.Now()
	e := cache.UserEntry{Name: name, UID: uid, GID: uid, Data: mustJSON(t, resp), FetchedAt: now, ExpiresAt: now.Add(f.cfg.TTLUser()), Stale: stale}
	if err := f.store.PutUser(t.Context(), e); err != nil {
		t.Fatal(err)
	}
}

// groupStale reports whether the cached group named name is marked stale.
func groupStale(t *testing.T, f *fixture, name string) bool {
	t.Helper()
	e, err := f.store.GroupByName(t.Context(), name)
	if err != nil || e == nil {
		t.Fatalf("group %s: %+v, %v", name, e, err)
	}
	return e.Stale
}

// userStale reports whether the cached user named name is marked stale.
func userStale(t *testing.T, f *fixture, name string) bool {
	t.Helper()
	e, err := f.store.UserByName(t.Context(), name)
	if err != nil || e == nil {
		t.Fatalf("user %s: %+v, %v", name, e, err)
	}
	return e.Stale
}

// withStore runs body over a fixture on each kind of store.
func withStore(t *testing.T, mutate func(*config.Config), body func(t *testing.T, f *fixture)) {
	t.Helper()
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, mutate)
			if kind == "sqlite" {
				s, err := cache.OpenSQLite(t.Context(), t.TempDir()+"/cache.db")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = s.Close() })
				f.store, f.d.store = s, s
			}
			body(t, f)
		})
	}
}

func TestFreshUserShouldMarkContradictedGroupsStale(t *testing.T) {
	t.Parallel()
	alice := apitypes.NSSMembership{GID: 5001, Name: "alice"}
	devs := apitypes.NSSMembership{GID: 5100, Name: "devs"}
	// users collides with the local users group, so the policy drops it.
	users := apitypes.NSSMembership{GID: 2005, Name: "users"}

	tests := []struct {
		name          string
		group         apitypes.NSSGroupResponse
		stub          bool
		prev          []apitypes.NSSMembership // nil: alice not cached before
		fresh         []apitypes.NSSMembership
		ignoreMembers bool
		confirmed     bool // the server already confirmed alice is not in the group
		wantStale     bool
	}{
		{name: "should mark the group when a first fetch joins it and its cached list lacks the user", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, fresh: []apitypes.NSSMembership{alice, devs, ops}, wantStale: true},
		{name: "should mark the group when a refetch joins it and its cached list lacks the user", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, prev: []apitypes.NSSMembership{alice, devs}, fresh: []apitypes.NSSMembership{alice, devs, ops}, wantStale: true},
		{name: "should mark the group when a refetch leaves it and its cached list still names the user", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor", "alice"}}, prev: []apitypes.NSSMembership{alice, devs, ops}, fresh: []apitypes.NSSMembership{alice, devs}, wantStale: true},
		{name: "should not mark the group when the memberships are unchanged and agree", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor", "alice"}}, prev: []apitypes.NSSMembership{alice, devs, ops}, fresh: []apitypes.NSSMembership{alice, devs, ops}},
		{name: "should mark the group when an unchanged membership disagrees and the server has not confirmed it", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, prev: []apitypes.NSSMembership{alice, devs, ops}, fresh: []apitypes.NSSMembership{alice, devs, ops}, wantStale: true},
		{name: "should not mark the group when an unchanged membership disagrees and the server confirmed it", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, prev: []apitypes.NSSMembership{alice, devs, ops}, fresh: []apitypes.NSSMembership{alice, devs, ops}, confirmed: true},
		{name: "should mark the group when a joined membership disagrees even though the server confirmed it", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, prev: []apitypes.NSSMembership{alice, devs}, fresh: []apitypes.NSSMembership{alice, devs, ops}, confirmed: true, wantStale: true},
		{name: "should not mark the group when it is the user's primary group and the membership is unchanged", group: apitypes.NSSGroupResponse{Name: "alice", GID: 5001, Members: []string{"mnestor"}}, prev: []apitypes.NSSMembership{alice, devs}, fresh: []apitypes.NSSMembership{alice, devs}},
		{name: "should not mark the group when only a stub is cached and the membership is unchanged", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103}, stub: true, prev: []apitypes.NSSMembership{alice, ops}, fresh: []apitypes.NSSMembership{alice, ops}},
		{name: "should not mark the group when its truncated list lacks the user and the membership is unchanged", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}, MembersTruncated: true}, prev: []apitypes.NSSMembership{alice, ops}, fresh: []apitypes.NSSMembership{alice, ops}},
		{name: "should not mark the group when the policy drops an unchanged membership", group: apitypes.NSSGroupResponse{Name: "users", GID: 2005, Members: []string{"mnestor"}}, prev: []apitypes.NSSMembership{alice, users}, fresh: []apitypes.NSSMembership{alice, users}},
		{name: "should not mark the group when ignore_group_members is set and the membership is unchanged", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, prev: []apitypes.NSSMembership{alice, ops}, fresh: []apitypes.NSSMembership{alice, ops}, ignoreMembers: true},
		{name: "should not mark the group when a joined group's cached list already names the user", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"alice"}}, fresh: []apitypes.NSSMembership{alice, ops}},
		{name: "should not mark the group when a left group's cached list already lacks the user", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, prev: []apitypes.NSSMembership{alice, ops}, fresh: []apitypes.NSSMembership{alice}},
		{name: "should not mark the group when the policy drops the membership", group: apitypes.NSSGroupResponse{Name: "users", GID: 2005, Members: []string{"mnestor"}}, fresh: []apitypes.NSSMembership{alice, users}},
		{name: "should not mark the group when it is the user's primary group", group: apitypes.NSSGroupResponse{Name: "alice", GID: 5001, Members: []string{}}, fresh: []apitypes.NSSMembership{alice, devs}},
		{name: "should not mark the group when only a stub is cached", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103}, stub: true, fresh: []apitypes.NSSMembership{alice, ops}},
		{name: "should not mark the group when its truncated list lacks the user", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}, MembersTruncated: true}, fresh: []apitypes.NSSMembership{alice, ops}},
		{name: "should mark the group when its truncated list still names a user who left", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"alice"}, MembersTruncated: true}, prev: []apitypes.NSSMembership{alice, ops}, fresh: []apitypes.NSSMembership{alice}, wantStale: true},
		{name: "should not mark the group when ignore_group_members is set", group: apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, fresh: []apitypes.NSSMembership{alice, ops}, ignoreMembers: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			withStore(t, func(c *config.Config) { c.IgnoreGroupMembers = tt.ignoreMembers }, func(t *testing.T, f *fixture) {
				putGroup(t, f, tt.group, !tt.stub)
				if tt.prev != nil {
					putUser(t, f, "alice", 5001, tt.prev, true)
				}
				if tt.confirmed {
					f.d.contradictions.marked(tt.group.Name, "alice")
					f.d.contradictions.groupStored(tt.group.Name, tt.group.Members, false, true)
				}
				f.up.users["alice"].Groups = tt.fresh
				if r := one(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); r.Error != "" {
					t.Fatalf("got %s", r.Error)
				}
				if got := groupStale(t, f, tt.group.Name); got != tt.wantStale {
					t.Fatalf("group stale %v, want %v", got, tt.wantStale)
				}
				c, err := f.store.Counts(t.Context())
				if want := map[bool]int{true: 1, false: 0}[tt.wantStale]; err != nil || c.StaleGroups != want || c.StaleUsers != 0 {
					t.Fatalf("got counts %+v, %v; want %d stale groups and no stale user", c, err, want)
				}
			})
		})
	}
}

func TestFreshUserShouldMarkGroupsStaleWhenAnyPathFetchesIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		fetch func(t *testing.T, f *fixture)
	}{
		{name: "should mark the group when a lookup fetches the user", fetch: func(t *testing.T, f *fixture) {
			f.clock.Advance(f.cfg.TTLUser())
			f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
		}},
		{name: "should mark the group when a background refresh fetches the user", fetch: func(t *testing.T, f *fixture) {
			f.d.refresh(t.Context(), userKey{name: "alice"}.String())
		}},
		{name: "should mark the group when an sshd login refreshes the user", fetch: func(t *testing.T, f *fixture) {
			f.clock.Advance(2 * time.Minute)
			f.call(t, MethodGetUserRecord, p("userName", "alice"), sshdPeer, false)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, nil)
			f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
			putGroup(t, f, apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, true)
			f.up.users["alice"].Groups = append(f.up.users["alice"].Groups, ops)
			tt.fetch(t, f)
			if !groupStale(t, f, "ops") {
				t.Fatal("should mark the group stale")
			}
			if !strings.Contains(f.logs.String(), "marked a cache entry stale") {
				t.Fatalf("should log the marking at debug, got %s", f.logs)
			}
		})
	}
}

func TestStaleGroupShouldListANewMemberWhenTheUserJoinedIt(t *testing.T) {
	t.Parallel()
	withStore(t, nil, func(t *testing.T, f *fixture) {
		// The owner's case: all-users cached with mnestor only, then the
		// user is added in the directory and fetched fresh.
		f.up.groups["ops"] = &apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}
		f.call(t, MethodGetMemberships, p("groupName", "ops"), rootPeer, true)
		f.up.groups["ops"] = &apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor", "alice"}}
		f.up.users["alice"].Groups = append(f.up.users["alice"].Groups, ops)
		f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
		before := f.up.callCount()
		var got []string
		for _, r := range f.call(t, MethodGetMemberships, p("groupName", "ops"), rootPeer, true) {
			var m membershipReply
			if err := json.Unmarshal(r.Parameters, &m); err != nil || r.Error != "" {
				t.Fatalf("reply %+v, %v", r, err)
			}
			got = append(got, m.UserName)
		}
		if f.up.callCount() != before+1 || strings.Join(got, ",") != "mnestor,alice" {
			t.Fatalf("got members %v after %d calls, want a refetch listing alice", got, f.up.callCount()-before)
		}
		if groupStale(t, f, "ops") {
			t.Fatal("the refetched entry should not be stale")
		}
	})
}

func TestStaleGroupShouldStillBeServedWhenTheServerIsUnreachable(t *testing.T) {
	t.Parallel()
	withStore(t, nil, func(t *testing.T, f *fixture) {
		putGroup(t, f, apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"mnestor"}}, true)
		f.up.users["alice"].Groups = append(f.up.users["alice"].Groups, ops)
		f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
		if !groupStale(t, f, "ops") {
			t.Fatal("should mark the group stale")
		}
		f.up.setErr(upstream.ErrOffline)
		before := f.up.callCount()
		replies := f.call(t, MethodGetMemberships, p("groupName", "ops"), rootPeer, true)
		if r := one(t, replies); r.Error != "" || !strings.Contains(string(r.Parameters), `"mnestor"`) {
			t.Fatalf("got %+v, want the cached member list", r)
		}
		if f.up.callCount() != before+1 || !groupStale(t, f, "ops") {
			t.Fatal("should have tried the server and kept the entry stale")
		}
	})
}

func TestForgottenUserShouldMarkTheGroupsListingItStale(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.up.users["alice"].Groups = append(f.up.users["alice"].Groups, ops)
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	putGroup(t, f, apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"alice"}}, true)
	delete(f.up.users, "alice")
	f.clock.Advance(f.cfg.TTLUser())
	if got := errorOf(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); got != ErrorNoRecordFound {
		t.Fatalf("got %q", got)
	}
	if !groupStale(t, f, "ops") {
		t.Fatal("should mark a group still listing a removed user stale")
	}
}

func TestForgottenUserShouldStillMarkItsGroupsStaleWhenTheCacheCannotDropTheUser(t *testing.T) {
	t.Parallel()
	withStore(t, nil, func(t *testing.T, f *fixture) {
		f.up.users["alice"].Groups = append(f.up.users["alice"].Groups, ops)
		f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
		putGroup(t, f, apitypes.NSSGroupResponse{Name: "ops", GID: 5103, Members: []string{"alice"}}, true)
		delete(f.up.users, "alice")
		f.clock.Advance(f.cfg.TTLUser())
		f.d.store = &flakyStore{Store: f.store, failUserDelete: true}
		if got := errorOf(t, f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)); got != ErrorNoRecordFound {
			t.Fatalf("got %q, want the removed user not found", got)
		}
		if !strings.Contains(f.logs.String(), `msg="cannot drop a user from the cache" user=alice`) {
			t.Fatalf("should log the failed drop, got %s", f.logs)
		}
		if !groupStale(t, f, "ops") {
			t.Fatal("should mark a group still listing the removed user stale")
		}
	})
}

func TestFreshGroupShouldHandleAChangedMemberWhoseCachedUserIsUnusable(t *testing.T) {
	t.Parallel()

	own := apitypes.NSSMembership{GID: 5001, Name: "alice"}
	devs := apitypes.NSSMembership{GID: 5100, Name: "devs"}

	// Each row's groups would give the opposite outcome if the entry were
	// usable, so only the unusable entry can explain the result.
	tests := []struct {
		name      string
		groups    []apitypes.NSSMembership // alice's cached groups, before any corruption
		corrupt   bool                     // alice's cached entry does not decode
		failRead  bool                     // the user cache cannot be read
		wantStale bool
		wantLog   string
	}{
		{name: "should mark the user stale when its cached entry does not decode",
			groups: []apitypes.NSSMembership{own, devs}, corrupt: true, wantStale: true},
		{name: "should log and leave the user when the user cache cannot be read",
			groups: []apitypes.NSSMembership{own}, failRead: true, wantLog: `msg="cannot read the user cache" user=alice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			withStore(t, nil, func(t *testing.T, f *fixture) {
				putUser(t, f, "alice", 5001, tt.groups, false)
				if tt.corrupt {
					now := f.clock.Now()
					e := cache.UserEntry{Name: "alice", UID: 5001, GID: 5001, Data: []byte("{"), FetchedAt: now, ExpiresAt: now.Add(f.cfg.TTLUser())}
					if err := f.store.PutUser(t.Context(), e); err != nil {
						t.Fatal(err)
					}
				}
				putGroup(t, f, apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"carol"}}, true)
				f.up.groups["devs"] = &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice", "carol"}}
				f.d.store = &flakyStore{Store: f.store, failUserRead: tt.failRead}
				f.d.refresh(t.Context(), groupKey{name: "devs"}.String())
				if got := userStale(t, f, "alice"); got != tt.wantStale {
					t.Fatalf("got alice stale %v, want %v", got, tt.wantStale)
				}
				if tt.wantLog != "" && !strings.Contains(f.logs.String(), tt.wantLog) {
					t.Fatalf("should log %q, got %s", tt.wantLog, f.logs)
				}
			})
		})
	}
}

func TestFreshGroupShouldMarkContradictedUsersStale(t *testing.T) {
	t.Parallel()
	devs := apitypes.NSSMembership{GID: 5100, Name: "devs"}
	own := func(name string, uid uint32) apitypes.NSSMembership {
		return apitypes.NSSMembership{GID: uid, Name: name}
	}

	tests := []struct {
		name      string
		prev      *apitypes.NSSGroupResponse // nil: no full entry before
		stub      bool
		fresh     []string
		users     map[string][]apitypes.NSSMembership // cached users and their groups
		wantStale []string
	}{
		{
			name:  "should mark an added and a removed member when neither cached user agrees",
			prev:  &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice", "carol"}},
			fresh: []string{"carol", "erin"},
			users: map[string][]apitypes.NSSMembership{
				"alice": {own("alice", 5001), devs}, "carol": {own("carol", 5003), devs}, "erin": {own("erin", 5005)},
			},
			wantStale: []string{"alice", "erin"},
		},
		{
			name:  "should not mark members whose cached groups already agree",
			prev:  &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice", "carol"}},
			fresh: []string{"carol", "erin"},
			users: map[string][]apitypes.NSSMembership{
				"alice": {own("alice", 5001)}, "carol": {own("carol", 5003), devs}, "erin": {own("erin", 5005), devs},
			},
		},
		{
			name:  "should not mark anyone when the member list is unchanged",
			prev:  &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice", "carol"}},
			fresh: []string{"alice", "carol"},
			users: map[string][]apitypes.NSSMembership{"alice": {own("alice", 5001)}, "carol": {own("carol", 5003)}},
		},
		{
			name:  "should not mark anyone when only a stub was cached",
			prev:  &apitypes.NSSGroupResponse{Name: "devs", GID: 5100},
			stub:  true,
			fresh: []string{"carol", "erin"},
			users: map[string][]apitypes.NSSMembership{"alice": {own("alice", 5001), devs}, "erin": {own("erin", 5005)}},
		},
		{
			name:  "should not mark anyone when nothing was cached",
			fresh: []string{"carol", "erin"},
			users: map[string][]apitypes.NSSMembership{"erin": {own("erin", 5005)}},
		},
		{
			name:  "should skip a changed member when the user is not cached",
			prev:  &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"alice"}},
			fresh: []string{"alice", "erin"},
			users: map[string][]apitypes.NSSMembership{"alice": {own("alice", 5001), devs}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			withStore(t, nil, func(t *testing.T, f *fixture) {
				uids := map[string]uint32{"alice": 5001, "carol": 5003, "erin": 5005}
				for name, groups := range tt.users {
					putUser(t, f, name, uids[name], groups, false)
				}
				if tt.prev != nil {
					putGroup(t, f, *tt.prev, !tt.stub)
				}
				f.up.groups["devs"] = &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: tt.fresh}
				f.d.refresh(t.Context(), groupKey{name: "devs"}.String())
				var got []string
				for _, name := range []string{"alice", "carol", "erin"} {
					if _, cached := tt.users[name]; cached && userStale(t, f, name) {
						got = append(got, name)
					}
				}
				if strings.Join(got, ",") != strings.Join(tt.wantStale, ",") {
					t.Fatalf("got stale users %v, want %v", got, tt.wantStale)
				}
				if groupStale(t, f, "devs") {
					t.Fatal("the fetched group should not be stale")
				}
			})
		})
	}
}

func TestStaleUserShouldDropARemovedGroupWhenIDAsksNext(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	f.call(t, MethodGetUserRecord, p("userName", "alice"), userPeer, false)
	f.call(t, MethodGetMemberships, p("groupName", "devs"), rootPeer, true)
	// alice leaves devs in the directory; the group is refetched first.
	f.up.groups["devs"] = &apitypes.NSSGroupResponse{Name: "devs", GID: 5100, Members: []string{"carol"}}
	f.up.users["alice"].Groups = []apitypes.NSSMembership{{GID: 5001, Name: "alice"}}
	f.d.refresh(t.Context(), groupKey{name: "devs"}.String())
	// initgroups, as `id alice` runs it.
	var got []string
	for _, r := range f.call(t, MethodGetMemberships, p("userName", "alice"), rootPeer, true) {
		var m membershipReply
		if err := json.Unmarshal(r.Parameters, &m); err != nil || r.Error != "" {
			t.Fatalf("reply %+v, %v", r, err)
		}
		got = append(got, m.GroupName)
	}
	if strings.Join(got, ",") != "alice" {
		t.Fatalf("got groups %v, want the refetched list without devs", got)
	}
}

// allUsers and soniaPrimary are the owner's groups: sonia's primary group
// is mnestor's, and every account is in all-users.
var (
	allUsers     = apitypes.NSSMembership{GID: 2005, Name: "all-users"}
	soniaPrimary = apitypes.NSSMembership{GID: 2004, Name: "mnestor"}
)

// setupSonia replays the owner's restart: all-users is fetched while the
// server lists only mnestor, then sonia's entry from before the restart,
// which already has all-users, is due a refetch. The server now answers
// sonia with groups, and all-users with members.
func setupSonia(t *testing.T, f *fixture, groups []apitypes.NSSMembership, members []string) {
	t.Helper()
	f.up.groups["all-users"] = &apitypes.NSSGroupResponse{Name: "all-users", GID: 2005, Members: []string{"mnestor"}}
	if got := groupMembers(t, f, "all-users"); strings.Join(got, ",") != "mnestor" {
		t.Fatalf("got members %v", got)
	}
	resp := userResp("sonia", 5010, apitypes.NSSStatusActive)
	resp.GID, resp.Groups = 2004, []apitypes.NSSMembership{soniaPrimary, allUsers}
	now := f.clock.Now()
	e := cache.UserEntry{Name: "sonia", UID: 5010, GID: 2004, Data: mustJSON(t, resp), FetchedAt: now, ExpiresAt: now.Add(f.cfg.TTLUser()), Stale: true}
	if err := f.store.PutUser(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	fresh := *resp
	fresh.Groups = groups
	f.up.users["sonia"] = &fresh
	f.up.groups["all-users"] = &apitypes.NSSGroupResponse{Name: "all-users", GID: 2005, Members: members}
}

// groupMembers lists a group's members through GetMemberships by group.
func groupMembers(t *testing.T, f *fixture, group string) []string {
	t.Helper()
	var got []string
	for _, r := range f.call(t, MethodGetMemberships, p("groupName", group), rootPeer, true) {
		var m membershipReply
		if err := json.Unmarshal(r.Parameters, &m); err != nil || r.Error != "" {
			t.Fatalf("reply %+v, %v", r, err)
		}
		got = append(got, m.UserName)
	}
	return got
}

// groupCalls counts the upstream fetches of the group named name.
func (f *fakeUpstream) groupCalls(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == "group/name/"+name {
			n++
		}
	}
	return n
}

// fetchUser forces one upstream fetch of the user named name, as the
// background refresh does.
func (f *fixture) fetchUser(t *testing.T, name string) {
	t.Helper()
	f.d.refresh(t.Context(), userKey{name: name}.String())
}

func TestFreshUserShouldRepairAGroupFetchedBeforeTheMembershipWhenThePreviousEntryAgreed(t *testing.T) {
	t.Parallel()
	withStore(t, nil, func(t *testing.T, f *fixture) {
		setupSonia(t, f, []apitypes.NSSMembership{soniaPrimary, allUsers}, []string{"mnestor", "sonia"})
		if r := one(t, f.call(t, MethodGetUserRecord, p("userName", "sonia"), userPeer, false)); r.Error != "" {
			t.Fatalf("got %s", r.Error)
		}
		if !groupStale(t, f, "all-users") {
			t.Fatal("should mark all-users stale although sonia's previous entry already had it")
		}
		before := f.up.groupCalls("all-users")
		if got := groupMembers(t, f, "all-users"); strings.Join(got, ",") != "mnestor,sonia" || f.up.groupCalls("all-users") != before+1 {
			t.Fatalf("got members %v after %d group fetches, want a refetch listing sonia", got, f.up.groupCalls("all-users")-before)
		}
		if f.d.contradictions.n != 0 {
			t.Fatalf("got %d pairs held, want the healed pair cleared", f.d.contradictions.n)
		}
	})
}

func TestLastingDisagreementShouldCostOneGroupRefetchWhenTheServerConfirmsIt(t *testing.T) {
	t.Parallel()
	withStore(t, nil, func(t *testing.T, f *fixture) {
		// The server keeps all-users without sonia, as it does for a group
		// it adds without a membership row.
		setupSonia(t, f, []apitypes.NSSMembership{soniaPrimary, allUsers}, []string{"mnestor"})
		f.fetchUser(t, "sonia")
		if !groupStale(t, f, "all-users") {
			t.Fatal("the first user fetch should mark the group stale")
		}
		groupMembers(t, f, "all-users")
		for i := range 3 {
			f.fetchUser(t, "sonia")
			if groupStale(t, f, "all-users") {
				t.Fatalf("user fetch %d marked a confirmed disagreement again", i+2)
			}
			groupMembers(t, f, "all-users")
		}
		if got := f.up.groupCalls("all-users"); got != 2 {
			t.Fatalf("got %d group fetches, want 2: the first state and one confirming refetch", got)
		}
	})
}

func TestConfirmedDisagreementShouldClearWhenTheGroupOrTheUserMoves(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		change    func(f *fixture) // the server's change after confirmation
		refresh   bool             // the group is refetched on its own, as its TTL does
		wantPairs int
		wantStale bool // after the next user fetch
		wantList  string
	}{
		{name: "should keep suppressing when the group is refetched with the same list", refresh: true, wantPairs: 1, wantList: "mnestor"},
		{
			name:     "should clear the pair when the refetched group names the user",
			change:   func(f *fixture) { f.up.groups["all-users"].Members = []string{"mnestor", "sonia"} },
			refresh:  true,
			wantList: "mnestor,sonia",
		},
		{
			name:     "should clear the pair when the refetched group is truncated",
			change:   func(f *fixture) { f.up.groups["all-users"].MembersTruncated = true },
			refresh:  true,
			wantList: "mnestor",
		},
		{
			name:      "should weigh the membership again when the refetched group's list changed",
			change:    func(f *fixture) { f.up.groups["all-users"].Members = []string{"mnestor", "erin"} },
			refresh:   true,
			wantPairs: 1, // marked again by the next user fetch
			wantStale: true,
			wantList:  "mnestor,erin",
		},
		{
			name:     "should clear the pair when the user leaves the group",
			change:   func(f *fixture) { f.up.users["sonia"].Groups = []apitypes.NSSMembership{soniaPrimary} },
			wantList: "mnestor",
		},
		{
			name:      "should stay suppressed until the group is refetched when the server starts listing the user",
			change:    func(f *fixture) { f.up.groups["all-users"].Members = []string{"mnestor", "sonia"} },
			wantPairs: 1,
			wantList:  "mnestor",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			withStore(t, nil, func(t *testing.T, f *fixture) {
				setupSonia(t, f, []apitypes.NSSMembership{soniaPrimary, allUsers}, []string{"mnestor"})
				f.fetchUser(t, "sonia")
				groupMembers(t, f, "all-users")
				if !f.d.contradictions.suppressed("all-users", "sonia") {
					t.Fatal("the refetch should confirm the pair")
				}
				if tt.change != nil {
					tt.change(f)
				}
				if tt.refresh {
					f.d.refresh(t.Context(), groupKey{name: "all-users"}.String())
				}
				f.fetchUser(t, "sonia")
				if got := groupStale(t, f, "all-users"); got != tt.wantStale {
					t.Fatalf("group stale %v, want %v", got, tt.wantStale)
				}
				if f.d.contradictions.n != tt.wantPairs {
					t.Fatalf("got %d pairs held, want %d", f.d.contradictions.n, tt.wantPairs)
				}
				if got := strings.Join(groupMembers(t, f, "all-users"), ","); got != tt.wantList {
					t.Fatalf("got members %s, want %s", got, tt.wantList)
				}
			})
		})
	}
}

// flakyStore wraps a Store and fails the operations its flags name.
type flakyStore struct {
	cache.Store
	failMarkStale, failGroupRead, failUserRead, failUserDelete bool
}

// UserByName fails when failUserRead is set.
func (s *flakyStore) UserByName(ctx context.Context, name string) (*cache.UserEntry, error) {
	if s.failUserRead {
		return nil, errStore
	}
	return s.Store.UserByName(ctx, name)
}

// DeleteUser fails when failUserDelete is set.
func (s *flakyStore) DeleteUser(ctx context.Context, name string) error {
	if s.failUserDelete {
		return errStore
	}
	return s.Store.DeleteUser(ctx, name)
}

// MarkStale fails when failMarkStale is set.
func (s *flakyStore) MarkStale(ctx context.Context, sel cache.Selector) (int, error) {
	if s.failMarkStale {
		return 0, errStore
	}
	return s.Store.MarkStale(ctx, sel)
}

// GroupByName fails when failGroupRead is set.
func (s *flakyStore) GroupByName(ctx context.Context, name string) (*cache.GroupEntry, error) {
	if s.failGroupRead {
		return nil, errStore
	}
	return s.Store.GroupByName(ctx, name)
}

func TestContradictionShouldOnlyBeRememberedWhenTheGroupIsStale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		alreadyStale  bool
		corrupt       bool
		failMarkStale bool
		failGroupRead bool
		wantPairs     int
		wantLog       string
	}{
		{name: "should remember the pair when the group was already stale", alreadyStale: true, wantPairs: 1},
		{name: "should not remember the pair when marking the group fails", failMarkStale: true, wantLog: "cannot mark a cache entry stale"},
		{name: "should not remember the pair when the group cache cannot be read", failGroupRead: true, wantLog: "cannot read the group cache"},
		{name: "should not remember the pair when the cached group does not decode", corrupt: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			withStore(t, nil, func(t *testing.T, f *fixture) {
				setupSonia(t, f, []apitypes.NSSMembership{soniaPrimary, allUsers}, []string{"mnestor"})
				if tt.corrupt {
					now := f.clock.Now()
					e := cache.GroupEntry{Name: "all-users", GID: 2005, Members: true, Data: []byte("{"), FetchedAt: now, ExpiresAt: now.Add(f.cfg.TTLGroup())}
					if err := f.store.PutGroup(t.Context(), e); err != nil {
						t.Fatal(err)
					}
				}
				if tt.alreadyStale {
					if _, err := f.store.MarkStale(t.Context(), cache.Selector{Groups: true, Name: "all-users"}); err != nil {
						t.Fatal(err)
					}
				}
				f.d.store = &flakyStore{Store: f.store, failMarkStale: tt.failMarkStale, failGroupRead: tt.failGroupRead}
				f.fetchUser(t, "sonia")
				if f.d.contradictions.n != tt.wantPairs {
					t.Fatalf("got %d pairs held, want %d", f.d.contradictions.n, tt.wantPairs)
				}
				if tt.wantLog != "" && !strings.Contains(f.logs.String(), tt.wantLog) {
					t.Fatalf("should log %q, got %s", tt.wantLog, f.logs)
				}
			})
		})
	}
}

func TestCorruptCachedGroupShouldCountAsNoListWhenTheGroupIsStored(t *testing.T) {
	t.Parallel()
	withStore(t, nil, func(t *testing.T, f *fixture) {
		now := f.clock.Now()
		e := cache.GroupEntry{Name: "devs", GID: 5100, Members: true, Data: []byte("{"), FetchedAt: now, ExpiresAt: now.Add(f.cfg.TTLGroup())}
		if err := f.store.PutGroup(t.Context(), e); err != nil {
			t.Fatal(err)
		}
		f.d.contradictions.marked("devs", "erin")
		f.d.contradictions.groupStored("devs", nil, false, true)
		f.d.refresh(t.Context(), groupKey{name: "devs"}.String())
		if f.d.contradictions.n != 0 {
			t.Fatalf("got %d pairs held, want the confirmed pair cleared as by a changed list", f.d.contradictions.n)
		}
	})
}
