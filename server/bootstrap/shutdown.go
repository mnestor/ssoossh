package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"github.com/italypaleale/go-kit/servicerunner"
)

type shutdownManager struct {
	fns []servicerunner.Service
}

// Add registers fns to be run when Run is called. Nil entries are ignored.
func (s *shutdownManager) Add(fns ...servicerunner.Service) {
	for _, fn := range fns {
		if fn == nil {
			continue
		}

		s.fns = append(s.fns, fn)
	}
}

// Run executes every registered service to completion, giving them up to 5
// seconds combined, independent of ctx's own cancellation. Errors are
// logged, not returned — shutdown proceeds regardless.
func (s *shutdownManager) Run(ctx context.Context) {
	// Cleanup functions are one-shot and must each run to completion independently, so we set WaitAll to true
	sr := servicerunner.NewServiceRunner(s.fns...)
	sr.WaitAll = true

	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer shutdownCancel()
	err := sr.Run(shutdownCtx)
	if err != nil {
		slog.ErrorContext(ctx, "Error shutting down services", slog.Any("error", err))
	}
}
