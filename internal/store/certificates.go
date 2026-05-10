// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"errors"
	"sync"
	"time"
)

type MemoryCertificatesStore struct {
	certs map[string]Certificate
	subs  []*Subscriber
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

func (s *MemoryCertificatesStore) findSub(id string) *Subscriber {
	for _, v := range s.subs {
		if v.ID == id {
			return v
		}
	}
	return nil
}

func (s *MemoryCertificatesStore) GetWait(id string) *Subscriber {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sub := s.findSub(id); sub != nil {
		return sub
	}

	sub := &Subscriber{
		ID:    id,
		Phone: make(chan error, 1),
	}

	s.subs = append(s.subs, sub)

	return sub
}

func (s *MemoryCertificatesStore) Create(c *Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.certs[c.ID]; ok {
		return &DuplicateKeyError{ID: c.ID}
	}

	c.CreatedAt = time.Now().UTC()
	s.certs[c.ID] = *c

	if sub := s.findSub(c.ID); sub != nil {
		sub.Phone <- nil
	}

	return nil
}

func (s *MemoryCertificatesStore) Reject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sub := s.findSub(id); sub != nil {
		sub.Phone <- errors.New("rejected")
	}

	return nil
}

func (s *MemoryCertificatesStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.certs, id)
	return nil
}

func (s *MemoryCertificatesStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.certs)
}
