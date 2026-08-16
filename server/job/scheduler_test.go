package job

// Test methodology: Scheduler wraps gocron, an in-process library with no
// external dependencies, so it's exercised directly with real (short-
// interval) jobs rather than mocks. Tests run in parallel except where they
// share timing assumptions about a single scheduler's Run loop.
//
// Two branches aren't covered here: NewScheduler's error return only fires
// if a gocron.SchedulerOption fails, and this wrapper passes none; and
// RemoveJob's errors.Join branch needs gocron's own RemoveJob to fail for a
// job this method's own Jobs() scan just found, which would require racing
// gocron's internal removal channel — not something reachable through the
// public API under test.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-co-op/gocron/v2"

	"github.com/mnestor/ssoossh/server/service"
)

func TestNewScheduler_ShouldSucceed(t *testing.T) {
	t.Parallel()

	s, err := NewScheduler()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected a non-nil scheduler")
	}
}

func TestRegisterJob_ShouldRunImmediatelyWhenRequested(t *testing.T) {
	t.Parallel()

	s, err := NewScheduler()
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	ran := make(chan struct{}, 1)
	err = s.RegisterJob(
		context.Background(),
		"TestJob",
		gocron.DurationJob(time.Hour),
		func(ctx context.Context) error {
			ran <- struct{}{}
			return nil
		},
		service.RegisterJobOpts{RunImmediately: true},
	)
	if err != nil {
		t.Fatalf("unexpected error registering job: %v", err)
	}

	s.scheduler.Start()
	defer s.scheduler.Shutdown() //nolint:errcheck

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the job to run immediately, it never fired")
	}
}

func TestRegisterJob_ShouldErrorOnInvalidJobDefinition(t *testing.T) {
	t.Parallel()

	s, err := NewScheduler()
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	err = s.RegisterJob(
		context.Background(),
		"BadJob",
		gocron.DurationJob(0), // zero interval is invalid
		func(ctx context.Context) error { return nil },
		service.RegisterJobOpts{},
	)
	if err == nil {
		t.Fatal("expected an error for an invalid job definition, got nil")
	}
	if !errors.Is(err, gocron.ErrDurationJobIntervalZero) {
		t.Errorf("got error %v, want it to wrap ErrDurationJobIntervalZero", err)
	}
}

func TestRegisterJob_ShouldApplyExtraOptions(t *testing.T) {
	t.Parallel()

	s, err := NewScheduler()
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	err = s.RegisterJob(
		context.Background(),
		"TaggedJob",
		gocron.DurationJob(time.Hour),
		func(ctx context.Context) error { return nil },
		service.RegisterJobOpts{ExtraOptions: []gocron.JobOption{gocron.WithTags("nightly")}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	jobs := s.scheduler.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 registered job, got %d", len(jobs))
	}
	tags := jobs[0].Tags()
	if len(tags) != 1 || tags[0] != "nightly" {
		t.Errorf("got tags %v, want [nightly]", tags)
	}
}

func TestRemoveJob_ShouldRemoveJobByName(t *testing.T) {
	t.Parallel()

	s, err := NewScheduler()
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	if err := s.RegisterJob(
		context.Background(),
		"RemoveMe",
		gocron.DurationJob(time.Hour),
		func(ctx context.Context) error { return nil },
		service.RegisterJobOpts{},
	); err != nil {
		t.Fatalf("unexpected error registering job: %v", err)
	}

	if err := s.RemoveJob("RemoveMe"); err != nil {
		t.Fatalf("unexpected error removing job: %v", err)
	}

	for _, j := range s.scheduler.Jobs() {
		if j.Name() == "RemoveMe" {
			t.Fatal("expected the job to be removed, but it's still registered")
		}
	}
}

func TestRemoveJob_ShouldReturnNilWhenNoJobMatches(t *testing.T) {
	t.Parallel()

	s, err := NewScheduler()
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	if err := s.RemoveJob("NoSuchJob"); err != nil {
		t.Errorf("expected nil error when no job matches, got %v", err)
	}
}

func TestJobWithObservability_ShouldReturnJobsErrorUnchanged(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("job failed")
	wrapped := jobWithObservability("TestJob", func(context.Context) error { return wantErr })

	if err := wrapped(context.Background()); !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestJobWithObservability_ShouldReturnNilOnSuccess(t *testing.T) {
	t.Parallel()

	wrapped := jobWithObservability("TestJob", func(context.Context) error { return nil })

	if err := wrapped(context.Background()); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestRun_ShouldStopWhenContextCanceled(t *testing.T) {
	t.Parallel()

	s, err := NewScheduler()
	if err != nil {
		t.Fatalf("failed to create scheduler: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Give Run a moment to reach scheduler.Start() before canceling.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected Run to return nil after shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected Run to return after context cancellation, it never did")
	}
}
