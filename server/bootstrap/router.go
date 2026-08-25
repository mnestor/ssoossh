package bootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/pires/go-proxyproto"
	sloggin "github.com/samber/slog-gin"
	"github.com/wader/gormstore/v2"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/controller"
	"github.com/mnestor/ssoossh/server/frontend"
	"github.com/mnestor/ssoossh/server/logging"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
)

// gormSessionStore adapts *gormstore.Store to gin-contrib's sessions.Store
// interface, which needs an Options(sessions.Options) method the base store
// does not provide. This mirrors gin-contrib/sessions/gorm's own unexported
// wrapper; we replicate it only so we can construct the store directly and
// own the cleanup goroutine's quit channel (see initEngine).
type gormSessionStore struct {
	*gormstore.Store
}

// Options applies gin-contrib session options to the underlying store.
func (s *gormSessionStore) Options(options sessions.Options) {
	s.SessionOpts = options.ToGorillaOptions()
}

// Server wraps the Gin router and the HTTP(S) server that serves it.
type Server struct {
	router      *gin.Engine
	appSrv      *http.Server
	running     atomic.Bool
	wg          sync.WaitGroup
	config      *config.Config
	useTLS      bool
	appListener net.Listener
	serveErr    chan error // receives the Serve/ServeTLS error unless it's a clean shutdown
}

// initRouter builds the Gin engine and middleware chain using a.config and
// a.svc, registers routes, and returns a Server ready to be run.
func (a *app) initRouter() (*Server, error) {
	r, err := a.initEngine()
	if err != nil {
		return nil, err
	}

	if err := a.registerRoutes(r); err != nil {
		return nil, err
	}

	return &Server{
		router: r,
		config: a.config,
		// useTLS is set later, by configureAppServerTransport.
	}, nil
}

// initEngine builds the Gin engine and its middleware chain (gin mode,
// trusted proxies, recovery/tracing/logging/error-handling, rate limiting,
// HSTS, health checks, sessions) using a.config and a.db. Route
// registration is registerRoutes's job.
func (a *app) initEngine() (*gin.Engine, error) {
	c := a.config

	switch {
	case testing.Testing():
		gin.SetMode(gin.TestMode)
	case !c.Production:
		gin.SetMode(gin.DebugMode)
	default:
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	// Always call this, including with an empty list. gin.New() does NOT
	// default to trusting nothing — it defaults to trusting 0.0.0.0/0 and
	// ::/0 with ForwardedByClientIP on, so ClientIP() returns the leftmost,
	// caller-supplied X-Forwarded-For value from any peer. SetTrustedProxies
	// with a nil/empty slice is the only way to get "trust no proxy".
	//
	// gin warns about this itself, but only from Run(), and this server calls
	// http.Server.Serve directly — so nothing would have printed.
	if err := r.SetTrustedProxies(c.HTTP.TrustedProxies); err != nil {
		return nil, fmt.Errorf("failed to configure http.trusted_proxies: %w", err)
	}

	// gin.Recovery, tracing, the access log, and ErrorHandlerMiddleware all
	// have to come before RateLimitMiddleware, not after: each works by
	// calling c.Next() and only doing its real work (ending the span,
	// logging the request, translating c.Errors into a response) once the
	// rest of the chain has run, so they must already be running
	// (registered earlier, wrapping everything after them) by the time
	// RateLimitMiddleware calls c.Error+c.Abort - otherwise a rate-limited
	// request would never get traced or logged, and its error would never
	// get translated into a 429. These are all cheap, though, so this is
	// still effectively "reject flooding clients before doing any other
	// per-request work" - the expensive stuff (HSTS, CORS/CSP, route
	// dispatch) all comes after the rate limiter instead.
	r.Use(gin.Recovery())

	if c.Traces {
		r.Use(otelgin.Middleware(version.Name))
	}

	r.Use(sloggin.NewWithConfig(logging.Tagged(logging.TagAccessLog), sloggin.Config{
		WithUserAgent:      c.HTTP.AccessLogging.WithUserAgent,
		WithClientIP:       c.HTTP.AccessLogging.WithClientIP,
		WithRequestHeader:  c.HTTP.AccessLogging.WithRequestHeader,
		WithRequestID:      c.HTTP.AccessLogging.WithRequestID,
		WithRequestBody:    c.HTTP.AccessLogging.WithRequestBody,
		WithResponseBody:   c.HTTP.AccessLogging.WithResponseBody,
		WithResponseHeader: c.HTTP.AccessLogging.WithResponseHeader,
		WithSpanID:         c.HTTP.AccessLogging.WithSpanID,
		WithTraceID:        c.HTTP.AccessLogging.WithTraceID,
	}))
	r.Use(middleware.NewErrorHandlerMiddleware().Add())

	// A rate_limit of 0 (or less) disables rate limiting entirely.
	if c.HTTP.RateLimit > 0 {
		rateLimitInterval := c.HTTP.RateDuration / time.Duration(c.HTTP.RateLimit)
		r.Use(middleware.NewRateLimitMiddleware().Add(rate.Every(rateLimitInterval), c.HTTP.RateLimit))
	}

	// Sent regardless of whether this server terminates TLS itself: a
	// reverse proxy in front may be the one doing TLS termination, in which
	// case this process only ever sees plain HTTP but the header still
	// needs to reach the browser. Browsers ignore the header over a
	// connection they see as plain HTTP anyway (RFC 6797), so sending it
	// unconditionally is harmless when there's no proxy, and some
	// deployments require it present even on the HTTP response regardless.
	// An empty http.hsts value disables it. Unlike the global middleware
	// block below, this sits before the health-check routes on purpose: the
	// header only adds information and must reach every response, /healthz
	// and /ping included.
	if c.HTTP.Hsts != "" {
		r.Use(middleware.NewHstsMiddleware(c.HTTP.Hsts).Add())
	}

	// basic health checks
	r.GET("/healthz", healthzHandler)
	r.GET("/ping", pingHandler)

	// Setup global middleware
	// The healthz and ping routes above predate these Use calls, so health
	// probes (often addressed by IP) stay reachable regardless of Host.
	r.Use(middleware.NewServerNameMiddleware().Add(c.HTTP.ServerName))
	r.Use(middleware.NewCacheControlMiddleware().Add())
	r.Use(middleware.NewCorsMiddleware().Add())
	r.Use(middleware.NewCspMiddleware().Add())
	r.Use(middleware.NewXFrameOptionsMiddleware().Add())
	r.Use(middleware.NewXContentTypeOptionsMiddleware().Add())
	r.Use(middleware.NewReferrerPolicyMiddleware().Add())

	sessionSecret, err := resolveSessionSecret(c, a.db)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve session secret: %w", err)
	}

	// gormstore.New AutoMigrates its own "sessions" table on a.db every
	// startup - a deliberate, narrow exception to the no-AutoMigrate
	// convention (see server/model/model.go and .claude/rules/go.md): that
	// table is entirely owned and queried by the gormstore library, not by
	// our own model/ or migrations, so there's no schema this project's
	// migrations need to track for it.
	//
	// Constructed directly rather than via gormsessions.NewStore because that
	// wrapper starts the periodic-cleanup goroutine on a quit channel it
	// never returns, leaving no way to stop it - a goroutine leak on every
	// shutdown that keeps the process alive (in-process restarts, tests). We
	// own the quit channel here and close it from a shutdown hook (see
	// a.stopSessionCleanup, wired in BootstrapServe).
	gs := gormstore.New(a.db, sessionSecret)
	sessionCleanupQuit := make(chan struct{})
	go gs.PeriodicCleanup(1*time.Hour, sessionCleanupQuit)
	a.stopSessionCleanup = func(context.Context) error {
		close(sessionCleanupQuit)
		return nil
	}
	sessionStore := &gormSessionStore{Store: gs}

	// The store's own defaults set only Path and MaxAge, which leaves
	// HttpOnly and Secure false and omits SameSite from the wire entirely.
	// For a cookie that authorizes certificate issuance, all three matter.
	opts, err := sessionCookieOptions(c)
	if err != nil {
		return nil, err
	}
	sessionStore.Options(opts)

	r.Use(sessions.Sessions("ssoossh_session", sessionStore))

	return r, nil
}

// healthzHandler answers the liveness probe.
//
// Named rather than inline so it can carry an OpenAPI annotation; registered
// ahead of the server-name check so probes addressing the server by IP still
// reach it.
//
// @Summary     Liveness probe
// @Description Registered ahead of the server-name check, so a probe addressing the
// @Description server by IP rather than by its configured name still reaches it.
// @Description
// @Description Note this is one of the two endpoints that does not use the {data, error}
// @Description envelope: it predates the convention and is consumed by orchestrators
// @Description rather than by this project's own clients.
// @Produce     json
// @Success     200 {object} openapidoc.HealthPayload "The server is up"
// @Router      /healthz [get]
func healthzHandler(gc *gin.Context) {
	gc.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// pingHandler answers the plain-text liveness probe.
//
// @Summary     Liveness probe, plain text
// @Description For probes that want a body they can string-match without parsing JSON.
// @Produce     plain
// @Success     200 {string} string "pong"
// @Router      /ping [get]
func pingHandler(gc *gin.Context) {
	gc.String(http.StatusOK, "pong")
}

// sessionCookieOptions builds the session cookie's attributes from config.
//
// HttpOnly is not configurable: the session cookie is never read by client
// script, so exposing it to JavaScript would only widen what an XSS can do.
func sessionCookieOptions(c *config.Config) (sessions.Options, error) {
	sameSite, err := parseSameSite(c.HTTP.CookieSameSite)
	if err != nil {
		return sessions.Options{}, err
	}

	// Secure follows the browser-visible scheme unless overridden. Marking a
	// cookie Secure over plain HTTP means the browser silently drops it, so
	// this cannot simply default to true without breaking local development.
	secure := c.HTTP.IsTLS()
	if c.HTTP.CookieSecure != nil {
		secure = *c.HTTP.CookieSecure
	}
	if !secure {
		slog.Warn("session cookie is not marked Secure, so browsers will send it over plain HTTP; set http.is_https (or http.cookie_secure) once TLS terminates in front of this server")
	}

	// MaxAge must always be set to something positive. Leaving it zero does
	// not fall back to the store's default: Store.Options replaces the whole
	// options struct, wiping the 30 days gormstore set at construction, and
	// the store then writes each session row with
	// expires_at = now + MaxAge seconds — that is, already expired — while
	// reads filter on `expires_at > now`. A zero here means every request
	// after login is unauthenticated.
	// The cookie attribute carries the idle window, not the absolute cap:
	// the sliding refresh in SessionAuthMiddleware reissues it on activity,
	// and the middleware enforces the absolute cap separately against the
	// login timestamp.
	maxAge := resolvedCookieIdleTimeout(c)

	opts := sessions.Options{
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
		MaxAge:   int(maxAge.Seconds()),
	}

	return opts, nil
}

// resolvedCookieMaxAge is the session lifetime actually in force: the
// configured cookie_max_age, or the fallback when it is unset. Split out of
// sessionCookieOptions because the sliding-expiry middleware needs the same
// number — the refresh must reissue the cookie with the lifetime the store
// was configured with, not a second constant that can drift.
func resolvedCookieIdleTimeout(c *config.Config) time.Duration {
	if c.HTTP.CookieIdleTimeout > 0 {
		return c.HTTP.CookieIdleTimeout
	}
	return defaultCookieIdleTimeout
}

// resolvedCookieMaxAge is the absolute session cap in force: the configured
// cookie_max_age, or the fallback when it is unset. Enforced server-side by
// SessionAuthMiddleware against the login timestamp, not by the cookie
// attribute — the cookie's own MaxAge carries the idle window, since that
// is what the sliding refresh reissues.
func resolvedCookieMaxAge(c *config.Config) time.Duration {
	if c.HTTP.CookieMaxAge > 0 {
		return c.HTTP.CookieMaxAge
	}
	return defaultCookieMaxAge
}

// defaultCookieIdleTimeout is how long a session survives without a request
// when http.cookie_idle_timeout is unset. The sliding refresh extends it on
// activity, so this is the abandoned-browser window, not the working-day
// bound.
const defaultCookieIdleTimeout = 30 * time.Minute

// defaultCookieMaxAge is the absolute session cap when http.cookie_max_age
// is unset: a working day with margin, after which even an active session
// re-authenticates. Also the admin revocation window: group claims live in
// the session rather than the database, so a removed membership keeps
// working until the session ends. The sliding refresh deliberately does not
// re-check groups; this cap is what bounds how stale they can get, which is
// why it must stay absolute rather than sliding.
const defaultCookieMaxAge = 9 * time.Hour

// parseSameSite maps the configured name to its http.SameSite value. Empty
// means strict — see config.HTTPSettings.CookieSameSite for why that is the
// right default here.
func parseSameSite(name string) (http.SameSite, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "strict":
		return http.SameSiteStrictMode, nil
	case "lax":
		return http.SameSiteLaxMode, nil
	case "none":
		return http.SameSiteNoneMode, nil
	default:
		return 0, fmt.Errorf("invalid http.cookie_same_site %q, expected one of strict, lax, none", name)
	}
}

// registerRoutes registers the frontend static assets (if included) and
// every controller's routes on r, using a.svc.
func (a *app) registerRoutes(r *gin.Engine) error {
	err := frontend.RegisterFrontend(r)
	if errors.Is(err, frontend.ErrFrontendNotIncluded) {
		slog.Warn("Frontend is not included in the build. Skipping frontend registration.")
	} else if err != nil {
		return fmt.Errorf("failed to register frontend: %w", err)
	}

	sessionAuth := middleware.NewSessionAuthMiddleware(resolvedCookieIdleTimeout(a.config), resolvedCookieMaxAge(a.config)).Add()
	csrf := middleware.NewCsrfMiddleware(a.config.HTTP.PublicOrigin()).Add()
	adminAuth := middleware.NewAdminAuthMiddleware(a.config).Add()
	auditorAuth := middleware.NewAuditorAuthMiddleware(a.config).Add()

	// Browser-facing OIDC login/callback, outside /api since these are
	// redirects rather than JSON API calls.
	authGroup := r.Group("/auth")
	controller.NewAuthController(authGroup, a.svc.auth, csrf, a.config)

	// Set up API routes
	apiGroup := r.Group("/api")
	controller.NewCaController(apiGroup, a.svc.ca)

	// Load and validate logo image at startup
	logoImg, err := controller.LoadLogoImage(a.config.Branding.LogoPath)
	if err != nil {
		return fmt.Errorf("failed to load logo image: %w", err)
	}
	controller.NewBrandingController(apiGroup, a.config, logoImg)

	// Unauthenticated for the same reason as branding: the footer showing it
	// is on every page, the login page included.
	controller.NewVersionController(apiGroup)

	// Build per-endpoint rate limit middleware for certificate request creation.
	// Each endpoint gets its own rate limiter (per-IP, independent of each other).
	// These apply in addition to the global rate limit.
	var certRequestRateLimit *controller.CertRequestRateLimitMiddleware
	if !a.config.Production && a.config.HTTP.RateLimitDisableForDev {
		// Rate limiting disabled for dev when production=false
	} else {
		limiter := middleware.NewEndpointRateLimiter()
		certRequestRateLimit = &controller.CertRequestRateLimitMiddleware{}
		if a.config.HTTP.CertRequestRateLimit.User > 0 {
			certRequestRateLimit.User = limiter.PerIP(rate.Limit(a.config.HTTP.CertRequestRateLimit.User), 1)
		}
		if a.config.HTTP.CertRequestRateLimit.ServiceEnroll > 0 {
			certRequestRateLimit.ServiceEnroll = limiter.PerIP(rate.Limit(a.config.HTTP.CertRequestRateLimit.ServiceEnroll), 1)
		}
		if a.config.HTTP.CertRequestRateLimit.PAM > 0 {
			certRequestRateLimit.PAM = limiter.PerIP(rate.Limit(a.config.HTTP.CertRequestRateLimit.PAM), 1)
		}
	}
	controller.NewCertRequestController(apiGroup, a.svc.certRequest, sessionAuth, csrf, certRequestRateLimit)

	controller.NewUserController(apiGroup, a.config, sessionAuth, a.db)
	controller.NewNotificationController(apiGroup, a.svc.notification, sessionAuth, csrf)
	controller.NewCertificateController(apiGroup, a.svc.certificate, sessionAuth, a.config)

	// Build per-code rate limit middleware for service certificate redemption.
	// The limit is keyed on the enrollment code to protect against brute-forcing.
	var enrollmentRateLimit gin.HandlerFunc
	if !a.config.Production && a.config.HTTP.RateLimitDisableForDev {
		// Rate limiting disabled for dev when production=false
	} else if a.config.HTTP.ServiceCodeRateLimit.Limit > 0 {
		codeLimiter := middleware.NewEndpointRateLimiter()
		enrollmentRateLimit = codeLimiter.CodeBucket(
			rate.Limit(a.config.HTTP.ServiceCodeRateLimit.Limit),
			1,
			controller.ExtractEnrollmentCodeForRateLimit,
		)
	}
	controller.NewEnrollmentController(apiGroup, a.svc.enrollment, enrollmentRateLimit, sessionAuth)
	controller.NewAdminController(apiGroup, a.config, a.db, sessionAuth, adminAuth, auditorAuth, csrf)

	return nil
}

// resolveSessionSecret returns the key that signs and encrypts session
// cookies: c.HTTP.CookieKey when configured, otherwise one generated once
// and persisted in server_secrets.
//
// Persisting rather than regenerating per process is what makes sessions
// survive a restart. It also means instances sharing a database share the
// key, so a session stays valid whichever instance a request lands on —
// though an explicit cookie_key is still the clearer choice there, since it
// does not depend on which instance won the race to generate it.
func resolveSessionSecret(c *config.Config, db *gorm.DB) ([]byte, error) {
	if c.HTTP.CookieKey != "" {
		return []byte(c.HTTP.CookieKey), nil
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate a random session secret: %w", err)
	}

	// Upsert with a no-op conflict update (reassigning name to itself) so
	// RETURNING always yields a row, whether this call won or lost the
	// insert race: two instances starting together both generate a key and
	// both try to insert, and the no-op update means the loser's RETURNING
	// reports the winner's already-stored row rather than overwriting it
	// and invalidating every session the winner just issued.
	secret := model.ServerSecret{
		Name:      model.ServerSecretSessionKey,
		Value:     key,
		CreatedAt: time.Now(),
	}
	if err := db.Clauses(
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "name"}},
			DoUpdates: clause.AssignmentColumns([]string{"name"}),
		},
		clause.Returning{},
	).Create(&secret).Error; err != nil {
		return nil, fmt.Errorf("failed to persist the generated session secret: %w", err)
	}

	slog.Info("http.cookie_key is not configured; using a generated session secret persisted in the database")

	return secret.Value, nil
}

// Run the web server
// Note this function is blocking, and will return only when the servers are shut down via context cancellation.
func (s *Server) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("server is already running")
	}
	defer s.running.Store(false)
	defer s.wg.Wait()

	// App server
	err := s.startAppServer(ctx)
	if err != nil {
		return fmt.Errorf("failed to start app server: %w", err)
	}

	s.wg.Add(1)
	defer func() {
		// Handle graceful shutdown
		defer s.wg.Done()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := s.appSrv.Shutdown(shutdownCtx)
		shutdownCancel()
		if err != nil {
			// Log the error only (could be context canceled)
			slog.WarnContext(ctx,
				"app server shutdown error",
				slog.Any("error", err),
			)
		}
	}()

	// Block until the context is canceled or the app server fails
	select {
	case <-ctx.Done():
		// Servers are stopped with deferred calls
		return nil
	case err := <-s.serveErr:
		return fmt.Errorf("app server failed: %w", err)
	}
}

// startAppServer builds and starts the HTTP(S) server in a background
// goroutine, configuring TLS from s.config when a certificate and key are
// present, and notifies systemd that the service is ready.
func (s *Server) startAppServer(ctx context.Context) error {
	// Create the HTTP(S) server
	s.appSrv = &http.Server{
		// MaxHeaderBytes:    maxHeaderBytes,
		// ReadHeaderTimeout: 10 * time.Second,
		Handler: s.router,
	}
	if s.config.HTTP.UnixSocket == "" {
		s.appSrv.Addr = net.JoinHostPort(s.config.HTTP.Address, strconv.Itoa(s.config.HTTP.Port))
	}

	if err := s.configureAppServerTransport(); err != nil {
		return err
	}

	// Create the listener if we don't have one already (tests may set
	// s.appListener directly, e.g. to bind an ephemeral port up front).
	if s.appListener == nil {
		listener, err := buildListener(s.config)
		if err != nil {
			return err
		}
		s.appListener = listener
	}

	// Start the HTTP(S) server in a background goroutine
	slog.InfoContext(ctx, "App server started",
		slog.String("bind", s.config.HTTP.Address),
		slog.Int("port", s.config.HTTP.Port),
		slog.Bool("tls", s.useTLS),
	)

	s.serveErr = make(chan error, 1)
	s.wg.Add(1)
	go s.serveApp()

	// Notify systemd that we are ready
	if err := sdNotifyReady(); err != nil {
		// Log the error only
		slog.Warn("Unable to notify systemd that the service is ready", "error", err)
	}

	return nil
}

// configureAppServerTransport loads the TLS certificate (if configured) and
// sets s.appSrv.TLSConfig, or configures h2c for plaintext HTTP/2 when no
// certificate/key pair is present. It also sets s.useTLS to reflect whether
// a usable certificate was found.
func (s *Server) configureAppServerTransport() error {
	var err error
	s.appSrv.TLSConfig, err = s.config.HTTP.TLS.Build(s.config.FIPSEnabled())
	if err != nil {
		return err
	}

	if s.appSrv.TLSConfig == nil {
		// Not using TLS: also need to enable HTTP/2 Cleartext (h2c)
		protocols := &http.Protocols{}
		protocols.SetHTTP1(true)
		protocols.SetUnencryptedHTTP2(true)
		s.appSrv.Protocols = protocols
		return nil
	}

	s.useTLS = true

	return nil
}

// buildListener returns the listener startAppServer serves on: a Unix
// domain socket when c.HTTP.UnixSocket is set, otherwise TCP, optionally
// wrapped to speak PROXY protocol when c.HTTP.ProxyProtocol is configured.
// The two are mutually exclusive (PROXY protocol is a TCP-connection
// concept, see config.HTTPSettings.ProxyProtocol).
func buildListener(c *config.Config) (net.Listener, error) {
	if c.HTTP.UnixSocket != "" {
		if len(c.HTTP.ProxyProtocol) > 0 {
			return nil, errors.New("http.unix_socket and http.proxy_protocol cannot both be set")
		}
		return unixSocketListener(c.HTTP.UnixSocket)
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(c.HTTP.Address, strconv.Itoa(c.HTTP.Port)))
	if err != nil {
		return nil, fmt.Errorf("failed to create TCP listener: %w", err)
	}

	if len(c.HTTP.ProxyProtocol) == 0 {
		return listener, nil
	}

	policy, err := proxyproto.TrustProxyHeaderFromRanges(c.HTTP.ProxyProtocol)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("invalid http.proxy_protocol range: %w", err)
	}

	return &proxyproto.Listener{Listener: listener, ConnPolicy: policy}, nil
}

// unixSocketListener listens on the Unix domain socket at path, removing a
// stale socket file left behind by an unclean previous shutdown first (a
// live socket file can't be bound to again).
func unixSocketListener(path string) (net.Listener, error) {
	if err := os.RemoveAll(path); err != nil {
		return nil, fmt.Errorf("failed to remove stale unix socket %q: %w", path, err)
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to create unix socket listener: %w", err)
	}

	return listener, nil
}

// serveApp runs the app server's Serve/ServeTLS loop until shutdown,
// forwarding any non-clean-shutdown error to s.serveErr. It is intended to
// run in its own goroutine, started by startAppServer.
func (s *Server) serveApp() {
	defer s.wg.Done()
	defer s.appListener.Close()

	var srvErr error
	if s.useTLS {
		srvErr = s.appSrv.ServeTLS(s.appListener, "", "")
	} else {
		srvErr = s.appSrv.Serve(s.appListener)
	}

	if !errors.Is(srvErr, http.ErrServerClosed) {
		s.serveErr <- srvErr
	}
}

// sdNotifyReady sends READY=1 to systemd's notification socket if
// NOTIFY_SOCKET is set, and is a no-op otherwise.
func sdNotifyReady() error {
	socketAddr := &net.UnixAddr{
		Name: os.Getenv("NOTIFY_SOCKET"),
		Net:  "unixgram",
	}

	if socketAddr.Name == "" {
		return nil
	}

	conn, err := net.DialUnix(socketAddr.Net, nil, socketAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err = conn.Write([]byte("READY=1")); err != nil {
		return err
	}

	return nil
}
