package bootstrap

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

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
	ca            *service.CAService
	auth          *service.AuthService
	certRequest   *service.CertRequestService
	certificate   *service.CertificateService
	host          *service.HostService
	enrollment    *service.EnrollmentService
	caKeyRegistry *service.CAKeyRegistry
}

// initServices constructs the services using a.config and a.httpClient,
// which must already be populated (see initObservability).
//
// Auth is the only constructor here that does network I/O (OIDC provider
// discovery, bounded by authDiscoveryTimeout); the rest are local and have
// no data dependency on it or on each other. Building all six concurrently
// means a slow discovery call no longer adds its own latency on top of the
// others' — cold start is bounded by the slowest one, not the sum.
func (a *app) initServices() (*services, error) {
	svc := &services{}
	svc.certificate = service.NewCertificateService(a.db)
	svc.caKeyRegistry = service.NewCAKeyRegistry(a.db, 15*time.Minute)

	g := new(errgroup.Group)

	g.Go(func() (err error) {
		svc.ca, err = service.NewCAService(a.httpClient, svc.caKeyRegistry)
		return err
	})

	g.Go(func() (err error) {
		authCtx, cancel := context.WithTimeout(context.Background(), authDiscoveryTimeout)
		defer cancel()
		svc.auth, err = service.NewAuthService(authCtx, a.config, a.db, a.httpClient)
		return err
	})

	g.Go(func() (err error) {
		svc.certRequest, err = service.NewCertRequestService(a.config, a.db, a.pubSub.Publisher, a.pubSub.Subscriber)
		return err
	})

	g.Go(func() (err error) {
		svc.host, err = service.NewHostService(a.config, a.db)
		return err
	})

	g.Go(func() (err error) {
		svc.enrollment, err = service.NewEnrollmentService(a.config)
		return err
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Validate lifetime policy configuration against reverse-proxy settings.
	// This is a startup check with logging only; bad config here is not an error.
	svc.certRequest.ValidateStartupConfig()

	return svc, nil
}
