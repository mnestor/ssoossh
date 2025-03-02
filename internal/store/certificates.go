// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"sync"
	"time"
)

type MemoryCertificatesStore struct {
	certs map[string]Certificate
	mu    sync.RWMutex
}

func NewMemoryCertificatesStore() *MemoryCertificatesStore {
	return &MemoryCertificatesStore{
		certs: map[string]Certificate{},
	}
}

func (s *MemoryCertificatesStore) Get(id string) (*Certificate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.certs[id]
	if !ok {
		return nil, &RecordNotFoundError{}
	}

	return &m, nil
}

func (s *MemoryCertificatesStore) Create(c *Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.certs[c.ID]; ok {
		return &DuplicateKeyError{ID: c.ID}
	}

	c.CreatedAt = time.Now().UTC()

	s.certs[c.ID] = *c
	return nil
}

func (s *MemoryCertificatesStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.certs, id)
	return nil
}
