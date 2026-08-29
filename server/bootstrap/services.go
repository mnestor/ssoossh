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
	enrollment    *service.EnrollmentService
	caKeyRegistry *service.CAKeyRegistry
	notification  *service.NotificationService
	audit         *service.AuditService
	// ldap is nil when directory enrichment is disabled, which every
	// consumer treats as "no enrichment" rather than branching on config.
	ldap *service.LDAPService
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
	svc.audit = service.NewAuditService(a.config, a.db)

	// Parsed here rather than inside the errgroup: a bad filter template is
	// a startup error, and failing before the concurrent constructors keeps
	// the message unmixed with theirs.
	ldapSvc, err := service.NewLDAPService(a.config, a.db)
	if err != nil {
		return nil, err
	}
	svc.ldap = ldapSvc
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
		svc.enrollment, err = service.NewEnrollmentService(a.config, a.db, a.pubSub.Publisher, a.pubSub.Subscriber)
		return err
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Notifications are wired after the group because they are a
	// dependency *of* two of its members rather than a peer: the
	// certificate paths publish through this, and publishing is all they
	// do — rendering and SMTP happen on the broker's own goroutines, so
	// nothing here puts a mail relay on the request path. See
	// service.NotificationService.Notify and initNotifications.
	svc.notification = service.NewNotificationService(a.db, a.pubSub.Publisher, a.config.Mail.Enabled)
	svc.certRequest.SetNotifier(svc.notification)
	svc.enrollment.SetNotifier(svc.notification)

	// The audit recorder is wired the same way and for the same reason: the
	// services that mutate state append their audit rows in their own
	// transactions, so they need the recorder rather than the other way
	// round.
	svc.certRequest.SetAuditor(svc.audit)
	svc.enrollment.SetAuditor(svc.audit)
	svc.auth.SetAuditor(svc.audit)
	svc.ldap.SetAuditor(svc.audit)
	svc.auth.SetLDAP(svc.ldap)

	// Validate lifetime policy configuration against reverse-proxy settings.
	// This is a startup check with logging only; bad config here is not an error.
	svc.certRequest.ValidateStartupConfig()

	return svc, nil
}
