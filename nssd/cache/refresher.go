package cache

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// refreshQueueSize bounds the pending background refreshes. At the
// default refresh_rate of 2/s it is minutes of work; past it a refresh is
// dropped and the entry simply refreshes on expiry instead.
const refreshQueueSize = 4096

// Refresher runs background refreshes one at a time, no faster than
// refresh_rate. Every source of background work shares it, the nowait
// refresh, the sweep and RefreshUser, so together they never exceed the
// rate (README R6). A key already queued is not queued twice.
type Refresher struct {
	wait  func(ctx context.Context) error
	run   func(ctx context.Context, key string)
	queue chan string

	mu      sync.Mutex
	pending map[string]struct{}
}

// NewRefresher returns a Refresher calling run for each key, at most
// perSecond times a second.
func NewRefresher(perSecond float64, run func(ctx context.Context, key string)) *Refresher {
	limiter := rate.NewLimiter(rate.Limit(perSecond), 1)
	return newRefresher(limiter.Wait, run, refreshQueueSize)
}

// newRefresher is NewRefresher with the rate wait and queue size
// supplied, so a test controls both.
func newRefresher(wait func(context.Context) error, run func(context.Context, string), size int) *Refresher {
	return &Refresher{wait: wait, run: run, queue: make(chan string, size), pending: map[string]struct{}{}}
}

// Enqueue asks for key to be refreshed and reports whether it was queued:
// false when it already is, or the queue is full.
func (r *Refresher) Enqueue(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pending[key]; ok {
		return false
	}
	select {
	case r.queue <- key:
		r.pending[key] = struct{}{}
		return true
	default:
		return false
	}
}

// Pending reports how many refreshes are queued.
func (r *Refresher) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// Run works through the queue until ctx ends.
func (r *Refresher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-r.queue:
			if err := r.wait(ctx); err != nil {
				return
			}
			// Out of pending before running, so a request that arrives
			// while the refresh runs queues the next one.
			r.mu.Lock()
			delete(r.pending, key)
			r.mu.Unlock()
			r.run(ctx, key)
		}
	}
}
