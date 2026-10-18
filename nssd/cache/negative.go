package cache

import (
	"sync"
	"time"
)

// maxNegative bounds the negative cache. A local user can make up names
// faster than entry_negative_timeout ages them out; past the bound the
// expired entries are dropped, and if that is not enough, all of them, so
// the worst case is an extra upstream miss, never unbounded memory.
const maxNegative = 10000

// Negative remembers authoritative misses for entry_negative_timeout. It
// lives in memory only, as sssd's does: after a restart a miss costs one
// upstream call again.
type Negative struct {
	ttl   time.Duration
	clock Clock

	mu      sync.Mutex
	entries map[string]time.Time
}

// NewNegative returns an empty negative cache whose entries last ttl.
func NewNegative(ttl time.Duration, clock Clock) *Negative {
	return &Negative{ttl: ttl, clock: clock, entries: map[string]time.Time{}}
}

// Has reports whether key missed within the last ttl.
func (n *Negative) Has(key string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	expires, ok := n.entries[key]
	if !ok {
		return false
	}
	if !n.clock.Now().Before(expires) {
		delete(n.entries, key)
		return false
	}
	return true
}

// Add records a miss for key.
func (n *Negative) Add(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.clock.Now()
	if len(n.entries) >= maxNegative {
		for k, expires := range n.entries {
			if !now.Before(expires) {
				delete(n.entries, k)
			}
		}
		if len(n.entries) >= maxNegative {
			clear(n.entries)
		}
	}
	n.entries[key] = now.Add(n.ttl)
}

// Delete forgets the miss for key, when a positive answer arrives.
func (n *Negative) Delete(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.entries, key)
}

// Clear forgets every miss, for `ssoosshd-nss cache -E`.
func (n *Negative) Clear() {
	n.mu.Lock()
	defer n.mu.Unlock()
	clear(n.entries)
}

// Len returns how many misses are remembered, expired ones included.
func (n *Negative) Len() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.entries)
}
