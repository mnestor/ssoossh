package userdb

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/varlink"
	"github.com/mnestor/ssoossh/nssd/cache"
	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/upstream"
)

// getGroupRecord answers io.systemd.UserDatabase.GetGroupRecord.
func (d *Daemon) getGroupRecord(ctx context.Context, call varlink.Call) result {
	var p groupParams
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
	gid, hasGID, res, ok := id("gid", p.GID)
	if !ok {
		return res
	}
	if !hasGID && p.GroupName == nil {
		return failure(ErrorEnumerationNotSupported, nil)
	}

	key := groupKey{gid: gid, byGID: true}
	if !hasGID {
		key = groupKey{name: *p.GroupName}
	}
	g, err := d.resolveGroup(ctx, key, false)
	if err != nil {
		return resolutionFailure(err)
	}
	if (hasGID && g.GID != gid) || (p.GroupName != nil && g.Name != *p.GroupName) {
		return failure(ErrorConflictingRecordFound, nil)
	}
	if !matchesFilters(p.DispositionMask, p.GIDMin, p.GIDMax, g.GID) {
		return failure(ErrorNonMatchingRecordFound, nil)
	}
	return reply(groupRecordFor(g))
}

// getMemberships answers io.systemd.UserDatabase.GetMemberships: by user,
// one reply per group in the user's answer; by group, one reply per member
// from a group lookup; by both, one reply if the pair is a membership.
func (d *Daemon) getMemberships(ctx context.Context, call varlink.Call) result {
	var p membershipParams
	if res, ok := decodeParams(call, &p); !ok {
		return res
	}
	if res, ok := checkService(p.Service); !ok {
		return res
	}

	var pairs []any
	switch {
	case p.UserName != nil:
		u, err := d.resolveUser(ctx, userKey{name: *p.UserName}, false)
		if err != nil {
			return resolutionFailure(err)
		}
		for _, g := range u.Groups {
			if p.GroupName == nil || g == *p.GroupName {
				pairs = append(pairs, membershipReply{UserName: u.Name, GroupName: g})
			}
		}
	case p.GroupName != nil:
		g, err := d.resolveGroup(ctx, groupKey{name: *p.GroupName}, true)
		if err != nil {
			return resolutionFailure(err)
		}
		for _, m := range g.Members {
			pairs = append(pairs, membershipReply{UserName: m, GroupName: g.Name})
		}
	default:
		return failure(ErrorEnumerationNotSupported, nil)
	}

	switch {
	case len(pairs) == 0:
		return failure(ErrorNoRecordFound, nil)
	case len(pairs) > 1 && !call.More:
		// A stream needs more; nss-systemd always sets it. A caller that
		// did not ask for one gets the varlink error that says so.
		return failure(varlink.ErrorExpectedMore, nil)
	default:
		return result{replies: pairs}
	}
}

// resolveGroup is resolveUser for groups. needMembers is set for
// GetMemberships by group: an entry filled from a user's memberships knows
// no members, so it then counts as a miss.
func (d *Daemon) resolveGroup(ctx context.Context, key groupKey, needMembers bool) (policy.Group, error) {
	if err := d.checkGroupKey(key); err != nil {
		d.log.Debug("not served", "key", key.String(), "reason", err)
		return policy.Group{}, errNoRecord
	}
	if d.negative.Has(key.String()) {
		return policy.Group{}, errNoRecord
	}

	entry := d.cachedGroup(ctx, key)
	if entry != nil && needMembers && !entry.Members {
		entry = nil
	}
	now := d.clock.Now()
	if entry != nil {
		if fr := d.ttl.Freshness(entry.FetchedAt, entry.ExpiresAt, entry.Stale, now); fr != cache.Expired {
			if fr == cache.Refresh && entry.Members {
				d.refresher.Enqueue(key.String())
			}
			return d.groupFromEntry(entry)
		}
	}

	g, err := d.fetchGroup(ctx, key)
	if err == nil || errors.Is(err, errNoRecord) {
		return g, err
	}
	if entry != nil && d.ttl.ServableOffline(entry.FetchedAt, now) {
		return d.groupFromEntry(entry)
	}
	d.log.Debug("no answer for a lookup", "key", key.String(), "error", err)
	return policy.Group{}, errUnavailable
}

// checkGroupKey is the short-circuit for a group key.
func (d *Daemon) checkGroupKey(key groupKey) error {
	if key.byGID {
		return d.policy.CheckGID(key.gid)
	}
	return d.policy.CheckGroupName(key.name)
}

// cachedGroup reads the cache, a failure counting as a miss.
func (d *Daemon) cachedGroup(ctx context.Context, key groupKey) *cache.GroupEntry {
	var entry *cache.GroupEntry
	var err error
	if key.byGID {
		entry, err = d.store.GroupByGID(ctx, key.gid)
	} else {
		entry, err = d.store.GroupByName(ctx, key.name)
	}
	if err != nil {
		d.log.Error("cannot read the group cache", "key", key.String(), "error", err)
		return nil
	}
	return entry
}

// groupFromEntry decodes a cached group, or takes a membership entry's
// name and gid, and runs the policy over it again.
func (d *Daemon) groupFromEntry(entry *cache.GroupEntry) (policy.Group, error) {
	if !entry.Members {
		if err := d.policy.GroupStub(apitypes.NSSMembership{GID: entry.GID, Name: entry.Name}); err != nil {
			d.log.Debug("a cached group is refused by the host policy", "group", entry.Name, "reason", err)
			return policy.Group{}, errNoRecord
		}
		return policy.Group{Name: entry.Name, GID: entry.GID}, nil
	}
	var resp apitypes.NSSGroupResponse
	if err := json.Unmarshal(entry.Data, &resp); err != nil {
		d.log.Error("a cached group does not decode; ignoring it", "group", entry.Name, "error", err)
		return policy.Group{}, errUnavailable
	}
	g, err := d.policy.Group(&resp)
	if err != nil {
		d.log.Debug("a cached group is refused by the host policy", "group", entry.Name, "reason", err)
		return policy.Group{}, errNoRecord
	}
	return g, nil
}

// fetchGroup is fetchUser for groups.
func (d *Daemon) fetchGroup(ctx context.Context, key groupKey) (policy.Group, error) {
	detached := context.WithoutCancel(ctx)
	g, err := cache.Await(ctx, &d.flight, key.String(), func() (policy.Group, error) {
		return d.loadGroup(detached, key)
	})
	if errors.Is(err, cache.ErrStillRunning) {
		return g, errors.Join(errUnavailable, err)
	}
	return g, err
}

// loadGroup is loadUser for groups.
func (d *Daemon) loadGroup(ctx context.Context, key groupKey) (policy.Group, error) {
	var resp *apitypes.NSSGroupResponse
	var err error
	if key.byGID {
		resp, err = d.upstream.GroupByGID(ctx, key.gid)
	} else {
		resp, err = d.upstream.GroupByName(ctx, key.name)
	}
	switch {
	case errors.Is(err, upstream.ErrNotFound):
		d.forgetGroup(ctx, key)
		return policy.Group{}, errNoRecord
	case errors.Is(err, upstream.ErrInvalid):
		d.log.Warn("the server refused a name the daemon accepted", "key", key.String(), "error", err)
		return policy.Group{}, errNoRecord
	case err != nil:
		return policy.Group{}, errors.Join(errUnavailable, err)
	}

	if resp == nil || (key.byGID && resp.GID != key.gid) || (!key.byGID && resp.Name != key.name) {
		d.log.Warn("the server answered a lookup with a different record; refusing it", "key", key.String())
		d.forgetGroup(ctx, key)
		return policy.Group{}, errNoRecord
	}
	g, err := d.policy.Group(resp)
	if err != nil {
		d.log.Warn("refusing a group from the server", "group", resp.Name, "gid", resp.GID, "reason", err)
		d.forgetGroup(ctx, key)
		return policy.Group{}, errNoRecord
	}
	d.storeGroup(ctx, resp)
	return g, nil
}

// forgetGroup is forgetUser for groups.
func (d *Daemon) forgetGroup(ctx context.Context, key groupKey) {
	d.negative.Add(key.String())
	entry := d.cachedGroup(ctx, key)
	if entry == nil {
		return
	}
	if err := d.store.DeleteGroup(ctx, entry.Name); err != nil {
		d.log.Error("cannot drop a group from the cache", "group", entry.Name, "error", err)
	}
}

// storeGroup caches a group answer that passed the policy.
func (d *Daemon) storeGroup(ctx context.Context, resp *apitypes.NSSGroupResponse) {
	if old, err := d.store.GroupByName(ctx, resp.Name); err == nil {
		d.warnGroupMoved(old, resp.Name, resp.GID)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		// not covered: NSSGroupResponse always marshals.
		d.log.Error("cannot encode a group for the cache", "group", resp.Name, "error", err)
		return
	}
	now := d.clock.Now()
	entry := cache.GroupEntry{
		Name: resp.Name, GID: resp.GID, Data: data, Members: true,
		FetchedAt: now, ExpiresAt: now.Add(d.cfg.TTLGroup()),
	}
	if err := d.store.PutGroup(ctx, entry); err != nil {
		d.log.Error("cannot write the group cache", "group", resp.Name, "error", err)
	}
	d.negative.Delete(groupKey{name: resp.Name}.String())
	d.negative.Delete(groupKey{gid: resp.GID, byGID: true}.String())
}
