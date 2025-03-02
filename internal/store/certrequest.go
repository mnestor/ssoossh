// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type MemoryCertRequestStore struct {
	requests map[string]CertRequest
	mu       sync.RWMutex
}

func NewMemoryCertRequestStore() *MemoryCertRequestStore {
	return &MemoryCertRequestStore{
		requests: map[string]CertRequest{},
	}
}

func (s *MemoryCertRequestStore) Get(id string) (*CertRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.requests[id]
	if !ok {
		return nil, &RecordNotFoundError{}
	}

	return &m, nil
}

func (s *MemoryCertRequestStore) Create(c *CertRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := uuid.New().String()
	c.ID = id

	if _, ok := s.requests[c.ID]; ok {
		return &DuplicateKeyError{ID: c.ID}
	}

	c.CreatedAt = time.Now().UTC()

	s.requests[c.ID] = *c
	return nil
}

func (s *MemoryCertRequestStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.requests, id)
	return nil
}
