package policy

import (
	"fmt"
	"sync"
	"time"
)

// Bounds on what the engine remembers about refused directory groups.
const (
	// findingsShown is how many of each kind status lists; the rest are a
	// count.
	findingsShown = 20
	// findingsTracked bounds the keys remembered for warn-once. Past it a
	// new key is neither warned about nor counted, which only a directory
	// with thousands of broken groups reaches.
	findingsTracked = 10000
)

// NameCollision is a directory group whose name a local group with a
// different gid already has. The daemon serves neither its name nor its
// gid, and drops it from its members' memberships.
type NameCollision struct {
	// Name is the name both groups have.
	Name string `json:"name"`
	// GID is the directory group's gid.
	GID uint32 `json:"gid"`
	// LocalGID is the local group's gid; nil when its /etc/group line
	// gives none (a compat "+name" entry).
	LocalGID *uint32 `json:"local_gid,omitempty"`
	// FirstSeen is when this daemon run first met it.
	FirstSeen time.Time `json:"first_seen"`
}

// RefusedGroup is a directory group refused by the id range.
type RefusedGroup struct {
	// Name is the directory group's name.
	Name string `json:"name"`
	// GID is its gid.
	GID uint32 `json:"gid"`
	// Reason says why.
	Reason string `json:"reason"`
	// FirstSeen is when this daemon run first met it.
	FirstSeen time.Time `json:"first_seen"`
}

// Findings is what status shows of the refused directory groups since the
// daemon started or last took SIGHUP: the first findingsShown of each kind
// and how many more there are.
type Findings struct {
	// Collisions are the group name collisions.
	Collisions []NameCollision `json:"group_name_collisions,omitempty"`
	// MoreCollisions counts collisions beyond Collisions.
	MoreCollisions int `json:"more_group_name_collisions,omitempty"`
	// Refused are the groups refused by the id range.
	Refused []RefusedGroup `json:"refused_groups,omitempty"`
	// MoreRefused counts refusals beyond Refused.
	MoreRefused int `json:"more_refused_groups,omitempty"`
}

// findings remembers each collision and refusal once per daemon run, so
// each is logged once however often lookups meet it.
type findings struct {
	mu   sync.Mutex
	seen map[string]struct{}
	data Findings
}

// newFindings returns an empty record.
func newFindings() *findings {
	return &findings{seen: map[string]struct{}{}}
}

// first reports whether key is new, remembering it. The caller logs and
// records only on true.
func (f *findings) first(key string) bool {
	if _, ok := f.seen[key]; ok || len(f.seen) >= findingsTracked {
		return false
	}
	f.seen[key] = struct{}{}
	return true
}

// collision records c, reporting whether it is new.
func (f *findings) collision(c NameCollision) bool {
	local := "none"
	if c.LocalGID != nil {
		local = fmt.Sprint(*c.LocalGID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.first(fmt.Sprintf("collision\x00%s\x00%d\x00%s", c.Name, c.GID, local)) {
		return false
	}
	if len(f.data.Collisions) < findingsShown {
		f.data.Collisions = append(f.data.Collisions, c)
	} else {
		f.data.MoreCollisions++
	}
	return true
}

// refused records r, reporting whether it is new.
func (f *findings) refused(r RefusedGroup) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.first(fmt.Sprintf("refused\x00%s\x00%d", r.Name, r.GID)) {
		return false
	}
	if len(f.data.Refused) < findingsShown {
		f.data.Refused = append(f.data.Refused, r)
	} else {
		f.data.MoreRefused++
	}
	return true
}

// snapshot returns a copy of the record.
func (f *findings) snapshot() Findings {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.data
	out.Collisions = append([]NameCollision(nil), f.data.Collisions...)
	out.Refused = append([]RefusedGroup(nil), f.data.Refused...)
	return out
}

// clear forgets everything, so each finding is logged again when next met.
func (f *findings) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = map[string]struct{}{}
	f.data = Findings{}
}
