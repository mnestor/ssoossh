package bootstrap

import (
	"github.com/mnestor/ssoossh/server/service"
)

// services groups the business-logic services the router depends on.
type services struct {
	ca *service.CAService
}

// initServices constructs the services using a.config and a.httpClient,
// which must already be populated (see initObservability).
func (a *app) initServices() (svc *services, err error) {
	svc = &services{}

	if svc.ca, err = service.NewCAService(a.config, a.httpClient); err != nil {
		return nil, err
	}

	return svc, nil
}
