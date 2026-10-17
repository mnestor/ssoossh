// Package cache is ssoosshd-nss's record cache: a Store interface with a
// memory and a SQLite implementation, the TTL rules that say whether an
// entry is fresh, due a background refresh or expired, the in-memory
// negative cache, the singleflight that lets a slow fetch finish into the
// cache after its caller gave up, and the rate-limited refresh queue. Part
// 3 of docs/proposals/nss-directory ("Cache", "Request path" step 5).
//
// The trust model is sssd's: entries are trusted when loaded, and file
// permissions are the boundary.
package cache

import (
	"context"
	"errors"
	"time"
)

// Clock tells the time. The cache's TTL decisions all go through one, so a
// test moves time on instead of sleeping.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
}

// SystemClock is the real clock.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// UserEntry is one cached user.
type UserEntry struct {
	// Name is the login name, the primary key, matched byte for byte.
	Name string
	// UID is unique across entries.
	UID uint32
	// GID is the server's primary gid, kept beside the JSON so a change
	// can be noticed without decoding it.
	GID uint32
	// Data is the server's user JSON, as fetched.
	Data []byte
	// FetchedAt is when the server sent it.
	FetchedAt time.Time
	// ExpiresAt is FetchedAt plus the user TTL.
	ExpiresAt time.Time
	// Stale is set by `ssoosshd-nss cache`: the entry is refetched on its
	// next lookup but still served while the server is unreachable.
	Stale bool
}

// GroupEntry is one cached group.
type GroupEntry struct {
	// Name is the group name, the primary key.
	Name string
	// GID is unique across entries.
	GID uint32
	// Data is the server's group JSON when Members is set; nil for an
	// entry filled from a user's memberships.
	Data []byte
	// Members is set when the entry came from a group lookup and so knows
	// its members; a user lookup only gives a group's name and gid.
	Members bool
	// FetchedAt, ExpiresAt and Stale are as for UserEntry.
	FetchedAt time.Time
	ExpiresAt time.Time
	Stale     bool
}

// Selector picks the entries MarkStale marks.
type Selector struct {
	// Users and Groups pick the kinds.
	Users, Groups bool
	// Name, when set, picks one entry by name within those kinds.
	Name string
}

// Counts is how many entries the store holds.
type Counts struct {
	// Users and Groups count every entry.
	Users  int `json:"users"`
	Groups int `json:"groups"`
	// StaleUsers and StaleGroups count those marked stale.
	StaleUsers  int `json:"stale_users"`
	StaleGroups int `json:"stale_groups"`
}

// Store holds cached entries. A lookup that misses returns nil and no
// error. Put replaces whatever entries share the new one's name or id, so
// a rename or a moved id never leaves two entries claiming one key.
type Store interface {
	// UserByName returns the user named name.
	UserByName(ctx context.Context, name string) (*UserEntry, error)
	// UserByUID returns the user with uid.
	UserByUID(ctx context.Context, uid uint32) (*UserEntry, error)
	// PutUser stores e.
	PutUser(ctx context.Context, e UserEntry) error
	// DeleteUser drops the user named name, after the server said it no
	// longer exists.
	DeleteUser(ctx context.Context, name string) error
	// GroupByName returns the group named name.
	GroupByName(ctx context.Context, name string) (*GroupEntry, error)
	// GroupByGID returns the group with gid.
	GroupByGID(ctx context.Context, gid uint32) (*GroupEntry, error)
	// PutGroup stores e.
	PutGroup(ctx context.Context, e GroupEntry) error
	// DeleteGroup drops the group named name.
	DeleteGroup(ctx context.Context, name string) error
	// MarkStale marks the selected entries stale and returns how many.
	MarkStale(ctx context.Context, sel Selector) (int, error)
	// Users returns every user, for the background sweep.
	Users(ctx context.Context) ([]UserEntry, error)
	// Groups returns every group, for the background sweep.
	Groups(ctx context.Context) ([]GroupEntry, error)
	// Counts returns the entry counts.
	Counts(ctx context.Context) (Counts, error)
	// Close releases the store.
	Close() error
}

// Freshness is what an entry's age says to do with it.
type Freshness int

const (
	// Fresh entries are answered from the cache.
	Fresh Freshness = iota
	// Refresh entries are answered from the cache and refreshed in the
	// background: past nowait_percentage of their TTL.
	Refresh
	// Expired entries are fetched before answering, and answered from the
	// cache only when the server cannot be had.
	Expired
)

// TTL holds the time rules for entries already in the store.
type TTL struct {
	// NowaitPercentage is entry_cache_nowait_percentage; 0 turns the
	// background refresh off.
	NowaitPercentage int
	// ExpireOffline is cache_expire_offline; 0 means an expired entry may
	// be served offline for ever.
	ExpireOffline time.Duration
}

// Freshness classifies an entry fetched at fetchedAt, expiring at
// expiresAt, possibly marked stale, at now.
func (t TTL) Freshness(fetchedAt, expiresAt time.Time, stale bool, now time.Time) Freshness {
	if stale || !now.Before(expiresAt) {
		return Expired
	}
	if t.NowaitPercentage <= 0 {
		return Fresh
	}
	nowait := fetchedAt.Add(expiresAt.Sub(fetchedAt) * time.Duration(t.NowaitPercentage) / 100)
	if now.Before(nowait) {
		return Fresh
	}
	return Refresh
}

// ServableOffline reports whether an expired entry fetched at fetchedAt
// may still be answered while the server cannot be had.
func (t TTL) ServableOffline(fetchedAt, now time.Time) bool {
	return t.ExpireOffline <= 0 || now.Sub(fetchedAt) < t.ExpireOffline
}

// ErrStillRunning is a flight whose caller stopped waiting: the fetch goes
// on and completes into the cache, and the caller answers without it.
var ErrStillRunning = errors.New("the upstream fetch is still running")
