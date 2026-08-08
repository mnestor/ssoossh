package bootstrap

import (
	"context"
	"crypto/tls"
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

	"github.com/gin-gonic/gin"
	sloggin "github.com/samber/slog-gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"golang.org/x/time/rate"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/config/tlsutils"
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
// a.svc, and returns a Server ready to be run.
func (a *app) initRouter() (*Server, error) {
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

	r.Use(gin.Recovery())
	r.Use(middleware.NewErrorHandlerMiddleware().Add())

	if c.Traces {
		r.Use(otelgin.Middleware(version.Name))
	}

	// The server terminates TLS only when the config provides a complete
	// certificate/key pair; startAppServer makes the same decision when it
	// builds the listener.
	useTLS := c.HTTP.TLS.HasKeyPair()

	// Browsers ignore Strict-Transport-Security over plain HTTP (RFC 6797),
	// so the header is sent only when this server terminates TLS itself; an
	// empty http.hsts value disables it. Unlike the global middleware block
	// below, this sits before the health-check routes on purpose: the header
	// only adds information and must reach every response, /healthz and
	// /ping included.
	if useTLS && c.HTTP.Hsts != "" {
		r.Use(middleware.NewHstsMiddleware(c.HTTP.Hsts).Add())
	}

	// basic health checks
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})

	// A rate_limit of 0 (or less) disables rate limiting entirely.
	var apiMiddleware []gin.HandlerFunc
	if c.HTTP.RateLimit > 0 {
		rateLimitInterval := c.HTTP.RateDuration / time.Duration(c.HTTP.RateLimit)
		apiMiddleware = append(apiMiddleware, middleware.NewRateLimitMiddleware().Add(rate.Every(rateLimitInterval), c.HTTP.RateLimit))
	}

	// Setup global middleware
	// The healthz and ping routes above predate these Use calls, so health
	// probes (often addressed by IP) stay reachable regardless of Host.
	r.Use(middleware.NewServerNameMiddleware().Add(c.HTTP.ServerName))
	r.Use(middleware.NewCacheControlMiddleware().Add())
	r.Use(middleware.NewCorsMiddleware().Add())
	r.Use(middleware.NewCspMiddleware().Add())

	err := frontend.RegisterFrontend(r)
	if errors.Is(err, frontend.ErrFrontendNotIncluded) {
		slog.Warn("Frontend is not included in the build. Skipping frontend registration.")
	} else if err != nil {
		return nil, fmt.Errorf("failed to register frontend: %w", err)
	}

	// Set up API routes
	apiGroup := r.Group("/api", apiMiddleware...)
	controller.NewCaController(apiGroup, a.svc.ca)

	return &Server{
		router: r,
		config: c,
		useTLS: useTLS,
	}, nil
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
		Addr: net.JoinHostPort(s.config.HTTP.Address, strconv.Itoa(s.config.HTTP.Port)),
		// MaxHeaderBytes:    maxHeaderBytes,
		// ReadHeaderTimeout: 10 * time.Second,
		Handler: s.router,
	}

	if err := s.configureAppServerTransport(); err != nil {
		return err
	}

	// Create the listener if we don't have one already
	if s.appListener == nil {
		var err error
		s.appListener, err = net.Listen("tcp", s.appSrv.Addr)
		if err != nil {
			return fmt.Errorf("failed to create TCP listener: %w", err)
		}
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
// certificate/key pair is present. It also updates s.useTLS to reflect
// whether a usable certificate was found.
func (s *Server) configureAppServerTransport() error {
	var cert tls.Certificate
	var err error
	switch {
	case s.config.HTTP.TLS.Certificate != "" && s.config.HTTP.TLS.PrivateKey != "":
		cert, err = tls.X509KeyPair([]byte(s.config.HTTP.TLS.Certificate), []byte(s.config.HTTP.TLS.PrivateKey))
	case s.config.HTTP.TLS.CertificateFile != "" && s.config.HTTP.TLS.PrivateKeyFile != "":
		cert, err = tls.LoadX509KeyPair(s.config.HTTP.TLS.CertificateFile, s.config.HTTP.TLS.PrivateKeyFile)
	default:
		s.useTLS = false
	}
	if err != nil {
		return err
	}

	if !s.useTLS {
		// Not using TLS
		// Here we also need to enable HTTP/2 Cleartext (h2c)
		protocols := &http.Protocols{}
		protocols.SetHTTP1(true)
		protocols.SetUnencryptedHTTP2(true)
		s.appSrv.Protocols = protocols
		return nil
	}

	cipherSuites, err := tlsutils.CipherSuites(s.config.HTTP.TLS.CipherSuites)
	if err != nil {
		return err
	}
	minVersion, err := tlsutils.MinVersion(s.config.HTTP.TLS.TLSMinVersion)
	if err != nil {
		return err
	}
	curves, err := tlsutils.Curve(s.config.HTTP.TLS.CurveNames)
	if err != nil {
		return err
	}

	// Note tls.Config.ServerName is deliberately not set: it's a
	// client-side field the server ignores. The configured server_name
	// is enforced by middleware.ServerNameMiddleware instead.
	s.appSrv.TLSConfig = &tls.Config{
		Certificates:     []tls.Certificate{cert},
		MinVersion:       minVersion,
		CurvePreferences: curves,
		CipherSuites:     cipherSuites,
	}
	return nil
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
