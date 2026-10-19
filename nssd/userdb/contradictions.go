package userdb

import "sync"

// maxContradictions bounds the contradiction map. Evicting a pair is cheap:
// a pending pair is not confirmed by its group's refetch, and a confirmed
// one is no longer suppressed, so either costs at most one extra group
// refetch on a later user fetch, after which the pair is back.
const maxContradictions = 4096

// contradiction is one (group, user) pair a fresh user record contradicted:
// the record is in the group but the group's cached member list lacks the
// user.
type contradiction struct {
	// confirmed is set once a fresh copy of the group, fetched after the
	// pair was marked, still lacked the user: the server itself keeps the
	// disagreement (for example the directory gid's group it adds to a
	// user's groups under auto_private_groups without a membership row).
	confirmed bool
	// seq orders pairs by when they were last marked, for eviction.
	seq uint64
}

// contradictions remembers which (group, user) contradictions marked a
// group stale and which of them the server confirmed, so a lasting
// disagreement costs one extra group fetch, not one per user fetch. It is
// in memory only: a restart forgets it and costs one refetch per lasting
// pair. Safe for concurrent use.
//
// A pair's life:
//   - marked: a fresh user record's contradiction marked the group stale.
//   - confirmed: the next fresh copy of the group still lacks the user.
//     Further contradictions from unchanged memberships are suppressed.
//   - cleared: a fresh copy of the group names the user or is truncated,
//     or a confirmed pair's group comes back with a member list that
//     differs from the one it replaces (the group is in flux, so the next
//     contradiction is weighed again), or the user's record leaves the
//     group, or the pair is evicted.
type contradictions struct {
	mu      sync.Mutex
	max     int
	n       int
	seq     uint64
	byGroup map[string]map[string]*contradiction
}

// newContradictions returns an empty map holding at most maxPairs pairs.
func newContradictions(maxPairs int) *contradictions {
	return &contradictions{max: maxPairs, byGroup: map[string]map[string]*contradiction{}}
}

// suppressed reports whether a contradiction of group by user is one the
// server confirmed, and so is not to be marked again.
func (c *contradictions) suppressed(group, user string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.byGroup[group][user]
	return p != nil && p.confirmed
}

// marked records that a contradiction of group by user marked the group
// stale, as a pair awaiting confirmation. A pair already held goes back to
// awaiting confirmation. At the cap, the pair marked longest ago is
// evicted first.
func (c *contradictions) marked(group, user string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	if p := c.byGroup[group][user]; p != nil {
		p.confirmed, p.seq = false, c.seq
		return
	}
	if c.n >= c.max {
		c.evictOldestLocked()
	}
	users := c.byGroup[group]
	if users == nil {
		users = map[string]*contradiction{}
		c.byGroup[group] = users
	}
	users[user] = &contradiction{seq: c.seq}
	c.n++
}

// evictOldestLocked drops the pair marked longest ago. It scans every
// pair, which is bounded by max and only happens when a new pair arrives
// at the cap.
func (c *contradictions) evictOldestLocked() {
	var oldGroup, oldUser string
	var oldSeq uint64
	found := false
	for group, users := range c.byGroup {
		for user, p := range users {
			if !found || p.seq < oldSeq {
				oldGroup, oldUser, oldSeq, found = group, user, p.seq, true
			}
		}
	}
	if found {
		c.removeLocked(oldGroup, oldUser)
	}
}

// forget clears the pair for group and user, if held: the user's record
// left the group.
func (c *contradictions) forget(group, user string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(group, user)
}

// removeLocked drops one pair, if held.
func (c *contradictions) removeLocked(group, user string) {
	users := c.byGroup[group]
	if _, ok := users[user]; !ok {
		return
	}
	delete(users, user)
	c.n--
	if len(users) == 0 {
		delete(c.byGroup, group)
	}
}

// groupStored settles group's pairs against a fresh copy of the group from
// the server: members is its member list, truncated whether the server cut
// it, and changed whether it differs from the cached list it replaced (or
// there was none). A pair is cleared when the copy names the user or is
// truncated; an awaiting pair is otherwise confirmed; a confirmed pair is
// cleared when the list changed and kept when it did not.
func (c *contradictions) groupStored(group string, members []string, truncated, changed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	users := c.byGroup[group]
	if len(users) == 0 {
		return
	}
	listed := make(map[string]bool, len(members))
	for _, m := range members {
		listed[m] = true
	}
	for user, p := range users {
		switch {
		case truncated || listed[user]:
			c.removeLocked(group, user)
		case !p.confirmed:
			p.confirmed = true
		case changed:
			c.removeLocked(group, user)
		}
	}
}
