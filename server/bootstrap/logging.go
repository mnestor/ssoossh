package bootstrap

import (
	"github.com/italypaleale/go-kit/servicerunner"

	"github.com/mnestor/ssoossh/server/logging"
)

// initLogging builds the slog logger from a.config and installs it as the
// default (see logging.New), returning shutdown functions for every
// configured rotating log file so their goroutines/file handles are
// released cleanly on exit.
func (a *app) initLogging() ([]servicerunner.Service, error) {
	closeFns, err := logging.New(a.config)
	if err != nil {
		return nil, err
	}

	shutdownFns := make([]servicerunner.Service, 0, len(closeFns))
	for _, closeFn := range closeFns {
		shutdownFns = append(shutdownFns, closeFn)
	}
	return shutdownFns, nil
}
