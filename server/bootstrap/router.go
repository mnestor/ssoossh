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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	gormsessions "github.com/gin-contrib/sessions/gorm"
	"github.com/gin-gonic/gin"
	"github.com/pires/go-proxyproto"
	sloggin "github.com/samber/slog-gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"golang.org/x/time/rate"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/controller"
	"github.com/mnestor/ssoossh/server/frontend"
	"github.com/mnestor/ssoossh/server/middleware"
)

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

	// Empty TrustedProxies means gin trusts no proxy headers at all - the
	// zero-value-safe default. Only set when non-empty since
	// SetTrustedProxies(nil) is itself meaningful to gin (also "trust
	// nothing"), so this is just avoiding an unnecessary call either way.
	if len(c.HTTP.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(c.HTTP.TrustedProxies); err != nil {
			return nil, fmt.Errorf("failed to configure http.trusted_proxies: %w", err)
		}
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

	r.Use(sloggin.NewWithConfig(slog.With("type", "accesslog"), sloggin.Config{
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
	r.GET("/healthz", func(gc *gin.Context) {
		gc.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/ping", func(gc *gin.Context) {
		gc.String(http.StatusOK, "pong")
	})

	// Setup global middleware
	// The healthz and ping routes above predate these Use calls, so health
	// probes (often addressed by IP) stay reachable regardless of Host.
	r.Use(middleware.NewServerNameMiddleware().Add(c.HTTP.ServerName))
	r.Use(middleware.NewCacheControlMiddleware().Add())
	r.Use(middleware.NewCorsMiddleware().Add())
	r.Use(middleware.NewCspMiddleware().Add())

	sessionSecret, err := resolveSessionSecret(c)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve session secret: %w", err)
	}

	// expiredSessionCleanup=true starts a background goroutine that
	// periodically deletes expired rows. gormsessions.NewStore AutoMigrates
	// its own "sessions" table on a.db every startup - a deliberate,
	// narrow exception to the no-AutoMigrate convention (see
	// server/model/model.go and .claude/rules/go.md): that table is
	// entirely owned and queried by the gormstore library, not by our own
	// model/ or migrations, so there's no schema this project's migrations
	// need to track for it.
	sessionStore := gormsessions.NewStore(a.db, true, sessionSecret)
	r.Use(sessions.Sessions("ssoossh_session", sessionStore))

	return r, nil
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

	// Browser-facing OIDC login/callback, outside /api since these are
	// redirects rather than JSON API calls.
	authGroup := r.Group("/auth")
	controller.NewAuthController(authGroup, a.svc.auth)

	// Set up API routes
	apiGroup := r.Group("/api")
	controller.NewCaController(apiGroup, a.svc.ca)
	controller.NewCertRequestController(apiGroup, a.svc.certRequest, middleware.NewSessionAuthMiddleware().Add())
	controller.NewHostController(apiGroup, a.svc.host, middleware.NewHostCertAuthMiddleware().Add())
	controller.NewEnrollmentController(apiGroup, a.svc.enrollment)

	return nil
}

// resolveSessionSecret returns c.HTTP.CookieKey as raw bytes to key the
// session store's encryption/signing, or a freshly generated one if none is
// configured. A generated key is process-local only (see
// config.HTTPSettings.CookieKey's doc comment): every existing session
// becomes unreadable on restart, and it can't be shared across multiple
// server instances, so production deployments should set an explicit key.
func resolveSessionSecret(c *config.Config) ([]byte, error) {
	if c.HTTP.CookieKey != "" {
		return []byte(c.HTTP.CookieKey), nil
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate a random session secret: %w", err)
	}

	slog.Warn("http.cookie_key is not configured; using a random session secret for this process only - existing sessions will not survive a restart, and this won't work across multiple server instances")

	return key, nil
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
	s.appSrv.TLSConfig, err = s.config.HTTP.TLS.Build()
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
