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
}

// storeUser caches a user answer that passed the policy, together with a
// group entry for each membership, and logs a uid or gid change of a name
// already cached: the server only moves an id after an admin accepted it
// (README R38), but every host it reaches should say so.
func (d *Daemon) storeUser(ctx context.Context, resp *apitypes.NSSUserResponse) {
	if old, err := d.store.UserByName(ctx, resp.Name); err == nil && old != nil && (old.UID != resp.UID || old.GID != resp.GID) {
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

	for _, m := range resp.Groups {
		if d.policy.GroupStub(m) == nil {
			d.storeGroupStub(ctx, m, now)
		}
	}
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
