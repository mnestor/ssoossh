package userdb

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/varlink"
	"github.com/mnestor/ssoossh/nssd/cache"
	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/upstream"
)

// getUserRecord answers io.systemd.UserDatabase.GetUserRecord.
func (d *Daemon) getUserRecord(ctx context.Context, call varlink.Call, peer Peer) result {
	var p userParams
	if res, ok := decodeParams(call, &p); !ok {
		return res
	}
	if res, ok := checkService(p.Service); !ok {
		return res
	}
	if present(p.FuzzyNames) {
		return invalidParameter("fuzzyNames")
	}
	if present(p.UUID) {
		return invalidParameter("uuid")
	}
	uid, hasUID, res, ok := id("uid", p.UID)
	if !ok {
		return res
	}
	if !hasUID && p.UserName == nil {
		return failure(ErrorEnumerationNotSupported, nil)
	}

	// By uid when one is given, then the name is checked against what was
	// found, as systemd's own services do.
	key := userKey{uid: uid, byUID: true}
	if !hasUID {
		key = userKey{name: *p.UserName}
	}
	u, err := d.resolveUser(ctx, key, peer.SSHD)
	if err != nil {
		return resolutionFailure(err)
	}
	if (hasUID && u.UID != uid) || (p.UserName != nil && u.Name != *p.UserName) {
		return failure(ErrorConflictingRecordFound, nil)
	}
	if !matchesFilters(p.DispositionMask, p.UIDMin, p.UIDMax, u.UID) {
		return failure(ErrorNonMatchingRecordFound, nil)
	}
	if peer.SSHD && u.Locked {
		d.log.Warn("serving a disabled account to sshd", "user", u.Name, "status", u.Status)
	}
	return reply(userRecordFor(u, peer))
}

// matchesFilters applies a call's secondary filters to a found record:
// the disposition mask and the id range. nss-systemd sends none of them on
// keyed lookups; userdbctl may.
func matchesFilters(mask []string, lo, hi *int64, recordID uint32) bool {
	if len(mask) > 0 && !slices.Contains(mask, dispositionRegular) {
		return false
	}
	if lo != nil && int64(recordID) < *lo {
		return false
	}
	return hi == nil || int64(recordID) <= *hi
}

// resolutionFailure maps a failed resolution to its varlink error.
func resolutionFailure(err error) result {
	if errors.Is(err, errNoRecord) {
		return failure(ErrorNoRecordFound, nil)
	}
	return failure(ErrorServiceNotAvailable, nil)
}

// resolveUser is the request path for one user: the local short-circuit,
// the negative cache, the cache with its freshness rules (and, for sshd,
// the login refresh), then upstream within ctx's deadline, falling back on
// an expired entry while the directory cannot be had.
func (d *Daemon) resolveUser(ctx context.Context, key userKey, sshd bool) (policy.User, error) {
	if err := d.checkUserKey(key); err != nil {
		d.log.Debug("not served", "key", key.String(), "reason", err)
		return policy.User{}, errNoRecord
	}
	if d.negative.Has(key.String()) {
		return policy.User{}, errNoRecord
	}

	entry := d.cachedUser(ctx, key)
	now := d.clock.Now()
	if entry != nil {
		if fr := d.ttl.Freshness(entry.FetchedAt, entry.ExpiresAt, entry.Stale, now); fr != cache.Expired {
			if sshd && now.Sub(entry.FetchedAt) > d.cfg.LoginSoftTTL {
				if u, done, err := d.loginRefresh(ctx, key); done {
					return u, err
				}
			}
			if fr == cache.Refresh {
				d.refresher.Enqueue(key.String())
			}
			return d.userFromEntry(entry)
		}
	}

	u, err := d.fetchUser(ctx, key)
	if err == nil || errors.Is(err, errNoRecord) {
		return u, err
	}
	if entry != nil && d.ttl.ServableOffline(entry.FetchedAt, now) {
		return d.userFromEntry(entry)
	}
	d.log.Debug("no answer for a lookup", "key", key.String(), "error", err)
	return policy.User{}, errUnavailable
}

// loginRefresh is the sshd freshness rule: refresh synchronously within
// login_refresh_timeout. done is false when the refresh did not settle the
// answer (timed out, or the directory is unavailable), and the caller then
// answers from the cache; the fetch carries on into the cache either way.
func (d *Daemon) loginRefresh(ctx context.Context, key userKey) (policy.User, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.LoginRefreshTimeout)
	defer cancel()
	u, err := d.fetchUser(ctx, key)
	if err == nil || errors.Is(err, errNoRecord) {
		return u, true, err
	}
	return policy.User{}, false, nil
}

// checkUserKey is the short-circuit for a user key.
func (d *Daemon) checkUserKey(key userKey) error {
	if key.byUID {
		return d.policy.CheckUID(key.uid)
	}
	return d.policy.CheckUserName(key.name)
}

// cachedUser reads the cache. A store that fails is logged and treated as
// a miss, so a broken cache costs upstream calls, not answers.
func (d *Daemon) cachedUser(ctx context.Context, key userKey) *cache.UserEntry {
	var entry *cache.UserEntry
	var err error
	if key.byUID {
		entry, err = d.store.UserByUID(ctx, key.uid)
	} else {
		entry, err = d.store.UserByName(ctx, key.name)
	}
	if err != nil {
		d.log.Error("cannot read the user cache", "key", key.String(), "error", err)
		return nil
	}
	return entry
}

// userFromEntry decodes a cached user and runs the policy over it again,
// so overrides and edited lists apply without a cache flush.
func (d *Daemon) userFromEntry(entry *cache.UserEntry) (policy.User, error) {
	var resp apitypes.NSSUserResponse
	if err := json.Unmarshal(entry.Data, &resp); err != nil {
		d.log.Error("a cached user does not decode; ignoring it", "user", entry.Name, "error", err)
		return policy.User{}, errUnavailable
	}
	u, err := d.policy.User(&resp)
	if err != nil {
		d.log.Debug("a cached user is refused by the host policy", "user", entry.Name, "reason", err)
		return policy.User{}, errNoRecord
	}
	return u, nil
}

// fetchUser fetches a user through the singleflight, waiting until ctx
// ends. The fetch itself is detached from ctx and completes into the cache
// whether or not anyone is still waiting.
func (d *Daemon) fetchUser(ctx context.Context, key userKey) (policy.User, error) {
	detached := context.WithoutCancel(ctx)
	u, err := cache.Await(ctx, &d.flight, key.String(), func() (policy.User, error) {
		return d.loadUser(detached, key)
	})
	if errors.Is(err, cache.ErrStillRunning) {
		return u, errors.Join(errUnavailable, err)
	}
	return u, err
}

// loadUser is one upstream user fetch: classify the answer, check it is the
// record asked for, apply the policy before caching, and store it.
func (d *Daemon) loadUser(ctx context.Context, key userKey) (policy.User, error) {
	var resp *apitypes.NSSUserResponse
	var err error
	if key.byUID {
		resp, err = d.upstream.UserByUID(ctx, key.uid)
	} else {
		resp, err = d.upstream.UserByName(ctx, key.name)
	}
	switch {
	case errors.Is(err, upstream.ErrNotFound):
		d.forgetUser(ctx, key)
		return policy.User{}, errNoRecord
	case errors.Is(err, upstream.ErrInvalid):
		// The server and the daemon disagree on a name's validity: not
		// served, and not cached as a miss either.
		d.log.Warn("the server refused a name the daemon accepted", "key", key.String(), "error", err)
		return policy.User{}, errNoRecord
	case err != nil:
		return policy.User{}, errors.Join(errUnavailable, err)
	}

	if resp == nil || (key.byUID && resp.UID != key.uid) || (!key.byUID && resp.Name != key.name) {
		d.log.Warn("the server answered a lookup with a different record; refusing it", "key", key.String())
		d.forgetUser(ctx, key)
		return policy.User{}, errNoRecord
	}
	u, err := d.policy.User(resp)
	if err != nil {
		d.log.Warn("refusing a user from the server", "user", resp.Name, "uid", resp.UID, "reason", err)
		d.forgetUser(ctx, key)
		return policy.User{}, errNoRecord
	}
	d.storeUser(ctx, resp)
	return u, nil
}

// forgetUser records an authoritative miss for key and drops any cached
// entry for it, so a removed account is not resurrected from the cache
// when the server is later unreachable.
func (d *Daemon) forgetUser(ctx context.Context, key userKey) {
	d.negative.Add(key.String())
	entry := d.cachedUser(ctx, key)
	if entry == nil {
		return
	}
	if err := d.store.DeleteUser(ctx, entry.Name); err != nil {
		d.log.Error("cannot drop a user from the cache", "user", entry.Name, "error", err)
	}
	// The user is in no group any more, as far as this host is concerned:
	// a cached member list still naming them is refetched.
	d.staleGroupsForUser(ctx, entry.Name, 0, nil, nil, entry)
}

// storeUser caches a user answer that passed the policy, together with a
// group entry for each membership, and logs a uid or gid change of a name
// already cached: the server only moves an id after an admin accepted it
// (README R38), but every host it reaches should say so. A cached group
// whose member list the record's memberships contradict is marked stale
// (staleGroupsForUser).
func (d *Daemon) storeUser(ctx context.Context, resp *apitypes.NSSUserResponse) {
	old, err := d.store.UserByName(ctx, resp.Name)
	if err != nil {
		// A failed read only costs the comparisons below.
		old = nil
	}
	if old != nil && (old.UID != resp.UID || old.GID != resp.GID) {
		d.log.Warn("a refresh changed a cached user's ids",
			"user", resp.Name, "old_uid", old.UID, "uid", resp.UID, "old_gid", old.GID, "gid", resp.GID)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		// not covered: NSSUserResponse is strings, numbers and slices of
		// them, which always marshal.
		d.log.Error("cannot encode a user for the cache", "user", resp.Name, "error", err)
		return
	}
	now := d.clock.Now()
	entry := cache.UserEntry{
		Name: resp.Name, UID: resp.UID, GID: resp.GID, Data: data,
		FetchedAt: now, ExpiresAt: now.Add(d.cfg.TTLUser()),
	}
	if err := d.store.PutUser(ctx, entry); err != nil {
		d.log.Error("cannot write the user cache", "user", resp.Name, "error", err)
	}
	d.negative.Delete(userKey{name: resp.Name}.String())
	d.negative.Delete(userKey{uid: resp.UID, byUID: true}.String())

	served := make([]apitypes.NSSMembership, 0, len(resp.Groups))
	for _, m := range resp.Groups {
		if d.policy.GroupStub(m) == nil {
			d.storeGroupStub(ctx, m, now)
			served = append(served, m)
		}
	}
	d.staleGroupsForUser(ctx, resp.Name, resp.GID, served, resp.Groups, old)
}

// staleGroupsForUser keeps cached group member lists coherent with a fresh
// user record, without asking the server:
//
//   - each served group whose cached member list lacks the user, and
//   - each group left (in old, the cached entry the record replaced, but
//     not in the record) whose cached member list still names the user
//
// is marked stale, so its next lookup refetches it. Every served
// membership is weighed, changed or not: a group cached from a server
// state that did not yet list the user (a fresh server repopulating its
// memberships, say) is repaired by the user's next fetch even when the
// user's previous entry already had the group.
//
// A disagreement the server itself keeps (a group it adds to a user's
// list without a membership row, such as the directory gid's group under
// auto_private_groups, never names the user) would otherwise cost a group
// refetch on every user fetch. So each marked pair is remembered, and once
// the group's refetch still lacks the user (contradictions.groupStored),
// the pair is suppressed for memberships that have not changed since old.
// A membership joined (or every one when nothing was cached) is always
// weighed.
//
// served are the memberships that passed the policy and filled the group
// cache, current all the record's memberships; a membership the policy
// drops is not weighed but not left either. The primary group is never
// weighed: the server lists a group's explicit members only, as
// /etc/group does, so it says nothing about the list. Left groups matter
// most, since a removed member lingering in a cached list is the direction
// that grants too much.
//
// The mirror case is left alone: a group the record was not in, before or
// now, whose cached list still names the user. Finding it needs a scan of
// every cached group's JSON; the group-side check (staleUsersForGroup)
// and the group TTL bound it instead.
func (d *Daemon) staleGroupsForUser(ctx context.Context, user string, primaryGID uint32, served, current []apitypes.NSSMembership, old *cache.UserEntry) {
	if d.cfg.IgnoreGroupMembers {
		// No member list is served, so none can disagree.
		return
	}
	prev, hadPrev := entryMemberships(old)
	for _, m := range served {
		if m.GID == primaryGID {
			continue
		}
		joined := !hadPrev || !hasMembership(prev, m.Name)
		if !joined && d.contradictions.suppressed(m.Name, user) {
			continue
		}
		if d.staleGroupIf(ctx, m.Name, user, false, "a fresh user record is in the group but its cached member list lacks the user") {
			d.contradictions.marked(m.Name, user)
		}
	}
	for _, m := range prev {
		if !hasMembership(current, m.Name) {
			d.contradictions.forget(m.Name, user)
			d.staleGroupIf(ctx, m.Name, user, true, "a fresh user record leaves the group but its cached member list still names the user")
		}
	}
}

// staleGroupIf marks the cached group named group stale when its member
// list names user (listed) or lacks user (!listed), and reports whether
// the list so contradicts the record and is now stale (marked here or
// already). A stub knows no members and is left alone, and so is a
// truncated list that lacks the user, since the cap may be what left the
// user out.
func (d *Daemon) staleGroupIf(ctx context.Context, group, user string, listed bool, why string) bool {
	e, err := d.store.GroupByName(ctx, group)
	if err != nil {
		d.log.Error("cannot read the group cache", "group", group, "error", err)
		return false
	}
	if e == nil || !e.Members {
		return false
	}
	var g apitypes.NSSGroupResponse
	if err := json.Unmarshal(e.Data, &g); err != nil {
		// groupFromEntry reports an entry that does not decode.
		return false
	}
	if slices.Contains(g.Members, user) != listed || (!listed && g.MembersTruncated) {
		return false
	}
	if e.Stale {
		return true
	}
	return d.markStale(ctx, cache.Selector{Groups: true, Name: group}, why, "group", group, "user", user)
}

// staleUserIf marks the cached user named user stale when its memberships
// disagree with a fresh group record that says user is (member) or is not
// (!member) in group, so the user's group list (initgroups, `id`) is
// refetched on its next lookup. A user not cached, or already stale, needs
// nothing; one that does not decode is marked, since a refetch replaces it.
func (d *Daemon) staleUserIf(ctx context.Context, user, group string, member bool, why string) {
	e, err := d.store.UserByName(ctx, user)
	if err != nil {
		d.log.Error("cannot read the user cache", "user", user, "error", err)
		return
	}
	if e == nil || e.Stale {
		return
	}
	if groups, ok := entryMemberships(e); ok && hasMembership(groups, group) == member {
		return
	}
	d.markStale(ctx, cache.Selector{Users: true, Name: user}, why, "user", user, "group", group)
}

// markStale marks the entries sel picks stale and logs it at debug with
// why and attrs, reporting whether it did; a store that fails is logged at
// error.
func (d *Daemon) markStale(ctx context.Context, sel cache.Selector, why string, attrs ...any) bool {
	if _, err := d.store.MarkStale(ctx, sel); err != nil {
		d.log.Error("cannot mark a cache entry stale", append(attrs, "error", err)...)
		return false
	}
	d.log.Debug("marked a cache entry stale", append(attrs, "reason", why)...)
	return true
}

// entryMemberships decodes a cached user's memberships, as the server sent
// them. ok is false when there is no entry or it does not decode.
func entryMemberships(e *cache.UserEntry) (groups []apitypes.NSSMembership, ok bool) {
	if e == nil {
		return nil, false
	}
	var resp apitypes.NSSUserResponse
	if err := json.Unmarshal(e.Data, &resp); err != nil {
		return nil, false
	}
	return resp.Groups, true
}

// hasMembership reports whether groups has a membership named name.
func hasMembership(groups []apitypes.NSSMembership, name string) bool {
	return slices.ContainsFunc(groups, func(m apitypes.NSSMembership) bool { return m.Name == name })
}

// storeGroupStub caches a group's name and gid from a user's memberships.
// An entry from a group lookup with the same name and gid is kept, since
// it also knows the members.
func (d *Daemon) storeGroupStub(ctx context.Context, m apitypes.NSSMembership, now time.Time) {
	old, err := d.store.GroupByName(ctx, m.Name)
	if err == nil && old != nil && old.Members && old.GID == m.GID {
		return
	}
	d.warnGroupMoved(old, m.Name, m.GID)
	stub := cache.GroupEntry{Name: m.Name, GID: m.GID, FetchedAt: now, ExpiresAt: now.Add(d.cfg.TTLGroup())}
	if err := d.store.PutGroup(ctx, stub); err != nil {
		d.log.Error("cannot write the group cache", "group", m.Name, "error", err)
	}
	d.negative.Delete(groupKey{name: m.Name}.String())
	d.negative.Delete(groupKey{gid: m.GID, byGID: true}.String())
}

// warnGroupMoved logs a gid change of a cached group name.
func (d *Daemon) warnGroupMoved(old *cache.GroupEntry, name string, gid uint32) {
	if old != nil && old.GID != gid {
		d.log.Warn("a refresh changed a cached group's gid", "group", name, "old_gid", old.GID, "gid", gid)
	}
}
