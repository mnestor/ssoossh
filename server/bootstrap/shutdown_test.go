package bootstrap

// Test methodology: shutdownManager is exercised directly (it's an
// unexported type, this file is in package bootstrap). Add is verified
// both by inspecting s.fns directly and by observing which registered
// services Run actually invokes. Tests run in parallel (t.Parallel()).

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/italypaleale/go-kit/servicerunner"
)

func TestShutdownManager_Add_ShouldAppendNonNilServices(t *testing.T) {
	t.Parallel()

	var s shutdownManager
	svc1 := func(context.Context) error { return nil }
	svc2 := func(context.Context) error { return nil }

	s.Add(svc1, svc2)

	if len(s.fns) != 2 {
		t.Fatalf("got %d registered services, want 2", len(s.fns))
	}
}

func TestShutdownManager_Add_ShouldIgnoreNilServices(t *testing.T) {
	t.Parallel()

	var s shutdownManager
	svc := func(context.Context) error { return nil }

	s.Add(nil, svc, nil)

	if len(s.fns) != 1 {
		t.Fatalf("got %d registered services, want 1 (nils skipped)", len(s.fns))
	}
}

func TestShutdownManager_Run_ShouldRunEveryRegisteredServiceToCompletion(t *testing.T) {
	t.Parallel()

	var s shutdownManager
	var mu sync.Mutex
	var ran []string

	record := func(name string) servicerunner.Service {
		return func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			ran = append(ran, name)
			return nil
		}
	}
	s.Add(record("first"), record("second"))

	s.Run(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 {
		t.Fatalf("got %d services run, want 2 (ran=%v)", len(ran), ran)
	}
}

func TestShutdownManager_Run_ShouldNotPanicWhenAServiceErrors(t *testing.T) {
	t.Parallel()

	var s shutdownManager
	s.Add(func(context.Context) error { return errors.New("cleanup failed") })

	// Run only logs service errors; it must return normally rather than
	// panicking or propagating them to the caller.
	s.Run(context.Background())
}

func TestShutdownManager_Run_ShouldStillRunServicesWhenParentContextAlreadyCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var s shutdownManager
	ran := make(chan struct{}, 1)
	s.Add(func(context.Context) error {
		ran <- struct{}{}
		return nil
	})

	// Run derives its shutdown context via context.WithoutCancel, so an
	// already-canceled parent must not prevent registered services from
	// running.
	s.Run(ctx)

	select {
	case <-ran:
	default:
		t.Fatal("expected the registered service to run despite the parent context being canceled")
	}
}

func TestShutdownManager_Run_ShouldWaitForAllServicesEvenIfOneFinishesFirst(t *testing.T) {
	t.Parallel()

	var s shutdownManager
	var mu sync.Mutex
	var completed []string

	s.Add(
		func(context.Context) error {
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			defer mu.Unlock()
			completed = append(completed, "slow")
			return nil
		},
		func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			completed = append(completed, "fast")
			return nil
		},
	)

	s.Run(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(completed) != 2 {
		t.Fatalf("got %d completed services, want 2 (WaitAll should wait for both): %v", len(completed), completed)
	}
}
