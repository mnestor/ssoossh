package bootstrap

import (
	"context"
	"time"

	"github.com/mnestor/ssoossh/server/service"
)

// authDiscoveryTimeout bounds service.NewAuthService's OIDC provider
// discovery request. It deliberately does not inherit the app's run
// context: that context is canceled to signal shutdown, but discovery is a
// one-time startup step that should run to completion (or fail) on its own
// terms rather than being torn down by an unrelated shutdown signal.
//
// TODO: AuthService construction is disabled below (svc.auth stays nil) —
// re-enable this const along with it. Until then, router.go still registers
// auth routes against the nil svc.auth, which will panic if hit.
const authDiscoveryTimeout = 10 * time.Second

// services groups the business-logic services the router depends on.
type services struct {
	ca          *service.CAService
	auth        *service.AuthService
	certRequest *service.CertRequestService
	host        *service.HostService
	enrollment  *service.EnrollmentService
}

// initServices constructs the services using a.config and a.httpClient,
// which must already be populated (see initObservability).
func (a *app) initServices() (svc *services, err error) {
	svc = &services{}

	if svc.ca, err = service.NewCAService(a.config, a.httpClient); err != nil {
		return nil, err
	}

	authCtx, cancel := context.WithTimeout(context.Background(), authDiscoveryTimeout)
	defer cancel()
	if svc.auth, err = service.NewAuthService(authCtx, a.config, a.db, a.httpClient); err != nil {
		return nil, err
	}

	if svc.certRequest, err = service.NewCertRequestService(a.config, a.db); err != nil {
		return nil, err
	}

	if svc.host, err = service.NewHostService(a.config); err != nil {
		return nil, err
	}

	if svc.enrollment, err = service.NewEnrollmentService(a.config); err != nil {
		return nil, err
	}

	return svc, nil
}
