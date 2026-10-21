package bootstrap

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"

	"github.com/mnestor/ssoossh/internal/version"
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
	// nss is nil when the NSS directory is disabled, on the same terms as
	// ldap.
	nss *service.NSSDirectoryService
	// identity re-reads the stored half of a session identity on each
	// request. Always present: it answers the same question with or
	// without a directory, just from fewer sources.
	identity *service.IdentityService
	// detailLevels holds each person's web and email detail levels.
	detailLevels *service.DetailLevelService

	// echoCodes holds the bootstrap identity echo's one-time codes, and
	// userInfoHealth the UserInfo endpoint's shared health state.
	echoCodes      *service.IdentityEchoCodeService
	userInfoHealth *service.UserInfoHealthService

	// nssLookup answers the NSS directory lookups and the shell
	// self-service, nssKeys administers and verifies the fleet keys, and
	// nssUsage aggregates their use and raises the usage alerts. All three
	// are nil when nss.enabled is false, which the routes answer with
	// nss_disabled.
	nssLookup *service.NSSLookupService
	nssKeys   *service.NSSKeyService
	nssUsage  *service.NSSUsageService
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
	// After the LDAP service for the same reason: a bad NSS filter or gecos
	// template is a startup error with its own message.
	nssSvc, err := service.NewNSSDirectoryService(a.config, a.db)
	if err != nil {
		return nil, err
	}
	svc.nss = nssSvc
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
	// service.NotificationService.Notify and initNotifications. The
	// directory sync publishes through it too, to tell an admin it has
	// disabled them.
	svc.notification = service.NewNotificationService(a.db, a.pubSub.Publisher, a.config.Mail.Enabled, a.config.LDAP.Enabled,
		a.config.CertOptions.Service.AllowUserAccounts)
	svc.notification.SetAdminConfig(&a.config.Admin)
	// Who the admin mail reaches is narrowed by the directory exactly as a
	// session's roles are (ldap.authoritative_role_groups).
	svc.notification.SetRoleNarrower(svc.ldap)
	svc.certRequest.SetNotifier(svc.notification)
	svc.enrollment.SetNotifier(svc.notification)
	svc.ldap.SetNotifier(svc.notification)
	// The NSS directory mails the admins the uid and gid changes waiting
	// for them, and asks the notification service who the admins are.
	svc.nss.SetNotifications(svc.notification, svc.notification)

	// The audit recorder is wired the same way and for the same reason: the
	// services that mutate state append their audit rows in their own
	// transactions, so they need the recorder rather than the other way
	// round.
	svc.certRequest.SetAuditor(svc.audit)
	svc.enrollment.SetAuditor(svc.audit)
	svc.auth.SetAuditor(svc.audit)
	svc.ldap.SetAuditor(svc.audit)
	svc.nss.SetAuditor(svc.audit)
	svc.auth.SetLDAP(svc.ldap)

	// The UserInfo call's failure counter and pause live in the database,
	// so every instance counts toward one pause and every instance honours
	// it; the pause and resume mail goes to the admins.
	svc.userInfoHealth = service.NewUserInfoHealthService(a.db, svc.auth.UserInfoEndpoint(), a.config.HTTP.PublicOrigin())
	svc.userInfoHealth.SetAuditor(svc.audit)
	svc.userInfoHealth.SetNotifications(svc.notification, svc.notification, a.config.Mail.Enabled)
	svc.auth.SetUserInfoHealth(svc.userInfoHealth)

	svc.echoCodes = service.NewIdentityEchoCodeService(a.db)
	svc.echoCodes.SetAuditor(svc.audit)
	// A login links the person to the POSIX account their entry carries.
	svc.ldap.SetNSSDirectory(svc.nss)

	// Built last of the plain constructors because it reads through the
	// directory service, and after it so a nil svc.ldap here means the same
	// thing it means everywhere else.
	svc.identity = service.NewIdentityService(a.db, svc.ldap)
	svc.detailLevels = service.NewDetailLevelService(a.db, &a.config.Admin)

	if err := a.initNSSServices(svc); err != nil {
		return nil, err
	}

	// Validate lifetime policy configuration against reverse-proxy settings.
	// This is a startup check with logging only; bad config here is not an error.
	svc.certRequest.ValidateStartupConfig()

	return svc, nil
}

// initNSSServices builds the NSS directory's lookup, fleet key and usage
// services when nss.enabled is on, leaving all three nil otherwise. The
// metrics resolve from the global meter provider, which
// initObservability has installed by now (a no-op one when metrics are
// off).
func (a *app) initNSSServices(svc *services) error {
	if !a.config.NSS.Enabled {
		return nil
	}
	meter := otel.GetMeterProvider().Meter(version.TracerName)

	keys, err := service.NewNSSKeyService(a.config, a.db, meter)
	if err != nil {
		// not covered: fails only on a crypto/rand failure or a gauge
		// the meter refuses, and its name and unit are constants that pass
		// (see NewNSSKeyService).
		return err
	}
	keys.SetAuditor(svc.audit)
	keys.SetNotifier(svc.notification)
	svc.nssKeys = keys

	// The third argument is the directory refresher: the lookups ask it
	// for a name or id the database does not hold yet, an entry created
	// since the last sync. Without the NSS directory they answer from the
	// database alone; passed as an untyped nil then, so the lookups see no
	// refresher rather than a nil pointer behind an interface.
	if svc.nss != nil {
		svc.nssLookup = service.NewNSSLookupService(a.config, a.db, svc.nss)
	} else {
		svc.nssLookup = service.NewNSSLookupService(a.config, a.db, nil)
	}
	svc.nssLookup.SetAuditor(svc.audit)

	usage, err := service.NewNSSUsageService(a.config, a.db, keys, meter)
	if err != nil {
		// not covered: fails only on an instrument the meter refuses, and
		// the names and units are constants that pass (see
		// NewNSSUsageService).
		return err
	}
	usage.SetAuditor(svc.audit)
	svc.nssUsage = usage
	return nil
}
