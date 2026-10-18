package cache

import (
	"context"
	"maps"
	"slices"
	"sync"
)

// MemoryStore is the cache_mode: memory Store. Nothing survives a restart.
type MemoryStore struct {
	mu          sync.RWMutex
	users       map[string]UserEntry
	usersByUID  map[uint32]string
	groups      map[string]GroupEntry
	groupsByGID map[uint32]string
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:       map[string]UserEntry{},
		usersByUID:  map[uint32]string{},
		groups:      map[string]GroupEntry{},
		groupsByGID: map[uint32]string{},
	}
}

// UserByName implements Store.
func (m *MemoryStore) UserByName(_ context.Context, name string) (*UserEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return copyOf(m.users, name), nil
}

// UserByUID implements Store.
func (m *MemoryStore) UserByUID(_ context.Context, uid uint32) (*UserEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	name, ok := m.usersByUID[uid]
	if !ok {
		return nil, nil
	}
	return copyOf(m.users, name), nil
}

// PutUser implements Store.
func (m *MemoryStore) PutUser(_ context.Context, e UserEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteUserLocked(e.Name)
	if other, ok := m.usersByUID[e.UID]; ok {
		m.deleteUserLocked(other)
	}
	e.Data = slices.Clone(e.Data)
	m.users[e.Name] = e
	m.usersByUID[e.UID] = e.Name
	return nil
}

// DeleteUser implements Store.
func (m *MemoryStore) DeleteUser(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteUserLocked(name)
	return nil
}

// deleteUserLocked drops a user and its uid index entry.
func (m *MemoryStore) deleteUserLocked(name string) {
	if old, ok := m.users[name]; ok {
		delete(m.usersByUID, old.UID)
		delete(m.users, name)
	}
}

// GroupByName implements Store.
func (m *MemoryStore) GroupByName(_ context.Context, name string) (*GroupEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return copyOf(m.groups, name), nil
}

// GroupByGID implements Store.
func (m *MemoryStore) GroupByGID(_ context.Context, gid uint32) (*GroupEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	name, ok := m.groupsByGID[gid]
	if !ok {
		return nil, nil
	}
	return copyOf(m.groups, name), nil
}

// PutGroup implements Store.
func (m *MemoryStore) PutGroup(_ context.Context, e GroupEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteGroupLocked(e.Name)
	if other, ok := m.groupsByGID[e.GID]; ok {
		m.deleteGroupLocked(other)
	}
	e.Data = slices.Clone(e.Data)
	m.groups[e.Name] = e
	m.groupsByGID[e.GID] = e.Name
	return nil
}

// DeleteGroup implements Store.
func (m *MemoryStore) DeleteGroup(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteGroupLocked(name)
	return nil
}

// deleteGroupLocked drops a group and its gid index entry.
func (m *MemoryStore) deleteGroupLocked(name string) {
	if old, ok := m.groups[name]; ok {
		delete(m.groupsByGID, old.GID)
		delete(m.groups, name)
	}
}

// MarkStale implements Store.
func (m *MemoryStore) MarkStale(_ context.Context, sel Selector) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	if sel.Users {
		n += markStale(m.users, sel.Name, func(e *UserEntry) { e.Stale = true })
	}
	if sel.Groups {
		n += markStale(m.groups, sel.Name, func(e *GroupEntry) { e.Stale = true })
	}
	return n, nil
}

// markStale applies mark to the entry named name, or to every entry when
// name is empty, and returns how many it marked.
func markStale[E any](entries map[string]E, name string, mark func(*E)) int {
	n := 0
	for key, e := range entries {
		if name != "" && key != name {
			continue
		}
		mark(&e)
		entries[key] = e
		n++
	}
	return n
}

// Users implements Store.
func (m *MemoryStore) Users(context.Context) ([]UserEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Collect(maps.Values(m.users)), nil
}

// Groups implements Store.
func (m *MemoryStore) Groups(context.Context) ([]GroupEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Collect(maps.Values(m.groups)), nil
}

// Counts implements Store.
func (m *MemoryStore) Counts(context.Context) (Counts, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := Counts{Users: len(m.users), Groups: len(m.groups)}
	for _, e := range m.users {
		if e.Stale {
			c.StaleUsers++
		}
	}
	for _, e := range m.groups {
		if e.Stale {
			c.StaleGroups++
		}
	}
	return c, nil
}

// Close implements Store.
func (m *MemoryStore) Close() error { return nil }

// copyOf returns a copy of entries[key], or nil.
func copyOf[E any](entries map[string]E, key string) *E {
	e, ok := entries[key]
	if !ok {
		return nil
	}
	return &e
}
