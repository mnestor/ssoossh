package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/tracing"
)

type Scheduler struct {
	scheduler gocron.Scheduler
}

// NewScheduler creates an idle Scheduler; call Run to start executing
// registered jobs.
func NewScheduler() (*Scheduler, error) {
	// Not unit-testable: gocron.NewScheduler's only error path comes from
	// validating SchedulerOptions, and this call passes none.
	scheduler, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("failed to create a new scheduler: %w", err)
	}

	return &Scheduler{
		scheduler: scheduler,
	}, nil
}

// RemoveJob dequeues every registered job with the given name, joining
// and returning any removal errors together.
func (s *Scheduler) RemoveJob(name string) error {
	jobs := s.scheduler.Jobs()

	var errs []error
	for _, job := range jobs {
		if job.Name() == name {
			// Not unit-testable: this ID was just read from the same Jobs()
			// call, so gocron's RemoveJob only fails here if a concurrent
			// caller removes the same job in between — a race against
			// gocron's internal removal channel, not reachable through the
			// public API under test.
			err := s.scheduler.RemoveJob(job.ID())
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to dequeue job %q with ID %v: %w", name, job.ID(), err))
			}
		}
	}

	return errors.Join(errs...)
}

// Run the scheduler.
// This function blocks until the context is canceled.
func (s *Scheduler) Run(ctx context.Context) error {
	slog.Info("Starting job scheduler")
	s.scheduler.Start()

	// Block until context is canceled
	<-ctx.Done()

	// Not unit-testable: gocron's Shutdown only errors after its internal
	// stopTimeout (10s, +2s grace) expires or on an internal stop-channel
	// error; NewScheduler doesn't expose a way to shorten that timeout, and
	// there's no seam to inject the internal error deterministically.
	err := s.scheduler.Shutdown()
	if err != nil {
		slog.Error("Error shutting down job scheduler", slog.Any("error", err))
	} else {
		slog.Info("Job scheduler shut down")
	}

	return nil
}

// RegisterJob schedules jobFn under name per def, wrapping it with
// tracing/logging (see jobWithObservability). opts.RunImmediately runs it
// once right away in addition to its schedule; opts.ExtraOptions are
// passed through to gocron unchanged.
func (s *Scheduler) RegisterJob(
	ctx context.Context,
	name string,
	def gocron.JobDefinition,
	jobFn func(ctx context.Context) error,
	opts service.RegisterJobOpts,
) error {

	// // Wrap the job in a handler that adds tracing and logging
	jobFn = jobWithObservability(name, jobFn)

	jobOptions := []gocron.JobOption{
		gocron.WithContext(ctx),
		gocron.WithName(name),
	}

	if opts.RunImmediately {
		jobOptions = append(jobOptions, gocron.JobOption(gocron.WithStartImmediately()))
	}

	jobOptions = append(jobOptions, opts.ExtraOptions...)

	_, err := s.scheduler.NewJob(def, gocron.NewTask(jobFn), jobOptions...)
	if err != nil {
		return fmt.Errorf("failed to register job %q: %w", name, err)
	}

	return nil
}

type (
	jobNameKey struct{}
	jobIDKey   struct{}
	jobFn      = func(ctx context.Context) error
)

// jobWithObservability wraps job with a per-run UUID, an OpenTelemetry
// span, and start/success/failure log lines, without changing its
// behavior or return value.
func jobWithObservability(jobName string, job jobFn) jobFn {
	return func(ctx context.Context) error {
		// Generate a random job ID
		jobID := uuid.NewString()

		// Save in the context
		ctx = context.WithValue(ctx, jobNameKey{}, jobName)
		ctx = context.WithValue(ctx, jobIDKey{}, jobID)

		// Create a new context with the span
		var err error
		ctx, span := tracing.Start(
			ctx,
			"nlapd.job."+jobName,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(
				tracing.JobID(jobID),
			),
		)
		defer tracing.End(span, err)

		// Log the start
		logger := slog.With(
			slog.String("name", jobName),
			slog.String("jobID", jobID),
		)
		start := time.Now()
		logger.DebugContext(ctx, "Starting job")

		// Run the job
		err = job(ctx)
		d := time.Since(start)
		if err != nil {
			logger.ErrorContext(ctx, "Job failed", slog.Any("error", err), slog.Duration("duration", d))
			return err
		}

		logger.DebugContext(ctx, "Job run successfully", slog.Duration("duration", d))
		return nil
	}
}
