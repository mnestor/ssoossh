// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"sync"
	"time"
)

type Subscriber struct {
	ID    string
	Phone chan int
}

var (
	subscriptions []*Subscriber
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

func findSub(id string) *Subscriber {
	for _, v := range subscriptions {
		if v.ID == id {
			return v
		}
	}
	return nil
}

func (s *MemoryCertificatesStore) GetWait(id string) *Subscriber {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sub := findSub(id); sub != nil {
		return sub
	}

	sub := &Subscriber{
		ID:    id,
		Phone: make(chan int),
	}

	subscriptions = append(subscriptions, sub)

	//there should be no way someone could pull this off
	//so i'm not going to really code it
	// cert, err := s.Get(id)
	// if err != nil {
	//   go func(){
	//       wait 2 seconds
	//       send cert into channel
	//     }()
	//   return channel
	// }

	return sub
}

func (s *MemoryCertificatesStore) Create(c *Certificate) error {
	s.mu.Lock()
	// defer unlock - we need to unlock before end so don't do it here

	if _, ok := s.certs[c.ID]; ok {
		s.mu.Unlock()
		return &DuplicateKeyError{ID: c.ID}
	}

	c.CreatedAt = time.Now().UTC()
	s.certs[c.ID] = *c
	s.mu.Unlock()

	if sub := findSub(c.ID); sub != nil {
		// someone is already listening so just send it
		sub.Phone <- 1
		return nil
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
