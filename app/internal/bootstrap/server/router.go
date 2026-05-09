package bootstrap

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	// gormsessions "github.com/gin-contrib/sessions/gorm"
	"github.com/gin-contrib/sessions/memstore"
	sloggin "github.com/gin-contrib/slog"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/app/server/config"
	"github.com/mnestor/ssoossh/internal/app/server/controller"
	"github.com/mnestor/ssoossh/internal/app/server/controller/api"
	"github.com/mnestor/ssoossh/internal/app/server/middleware"
	"github.com/mnestor/ssoossh/internal/app/server/middleware/auth"
	"github.com/mnestor/ssoossh/internal/common"
	"github.com/mnestor/ssoossh/internal/common/gormsession"
	"github.com/mnestor/ssoossh/internal/utils"
	"github.com/mnestor/ssoossh/internal/utils/systemd"
	"github.com/mnestor/ssoossh/web"
)

// This is used to register additional controllers for tests
var registerTestControllers []func(apiGroup *gin.RouterGroup, db *gorm.DB, svc *services)

func initRouter(ctx context.Context, db *gorm.DB, svc *services) (utils.Service, error) {
	cfg := ctx.Value(common.ContextConfig).(*config.Config)
	// Set the appropriate Gin mode based on the environment

	r := gin.New()
	initLogger(r)
	r.Use(gin.Recovery())

	if len(cfg.Server.TrustedProxies) == 0 {
		_ = r.SetTrustedProxies(nil)
	} else {
		_ = r.SetTrustedProxies(cfg.Server.TrustedProxies)
	}

	if cfg.Traces {
		r.Use(otelgin.Middleware(common.Name))
	}

	// rateLimitMiddleware := middleware.NewRateLimitMiddleware().Add(rate.Every(time.Second), 60)

	// Setup global middleware
	r.Use(middleware.CspMiddleware)
	r.Use(middleware.ErrorHandlerMiddleware)
	r.Use(middleware.NewDbMiddleware(db))

	session_sign_key := sha256.Sum256([]byte("sessionSigning"))
	session_encryption_key := sha256.Sum256([]byte("sessionEncrypt"))
	storeMem := memstore.NewStore(session_sign_key[:], session_encryption_key[:])
	storeGorm := gormsession.NewStore(db, true, session_sign_key[:], session_encryption_key[:])

	sessionStores := []sessions.SessionStore{
		{
			Name:  "login",
			Store: storeGorm,
		},
		{
			Name:  "default",
			Store: storeGorm,
		},
		{
			Name:  "default2",
			Store: storeMem,
		},
	}
	r.Use(sessions.SessionsManyStores(sessionStores))

	err := web.RegisterFrontend(r)
	if errors.Is(err, web.ErrFrontendNotIncluded) {
		slog.Warn("Frontend is not included in the build. Skipping frontend registration.")
	} else if err != nil {
		return nil, fmt.Errorf("failed to register frontend: %w", err)
	}

	oh := auth.NewOidcMiddleware(cfg)
	oh.RegisterHandlers(r, "/oauth")

	rBase := r.Group("/")
	controller.RegisterHandlers(rBase)

	apiGroup := r.Group("/api")
	{
		// because I keep trying to remove this...
		// oh.RequireLogin will redirect and abort
		// and we don't have a OptionalLogin method
		api.RegisterNoAuthHandlers(apiGroup)

		// now set everything else in /api to require authentication
		apiGroup.Use(oh.RequireLogin())

		// after required auth we can now check for authorization with casbin enforcer
		enforcer := auth.NewEnforcer(cfg)
		apiGroup.Use(enforcer.CheckAccessHandler())
		api.RegisterAuthHandlers(apiGroup)
	}

	// Set up the server
	srv := &http.Server{
		MaxHeaderBytes:    1 << 20,
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           r,
	}

	// Set up the listener
	network := "tcp"
	addr := net.JoinHostPort(cfg.Server.Address, strconv.Itoa(cfg.Server.Port))
	if cfg.Server.UnixSocket != "" {
		network = "unix"
		addr = cfg.Server.UnixSocket
		os.Remove(addr) // remove dangling the socket file to avoid file-exist error
	}

	listener, err := net.Listen(network, addr) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("failed to create %s listener: %w", network, err)
	}

	// Set the socket mode if using a Unix socket
	if network == "unix" && cfg.Server.UnixSocketMode != "" {
		mode, err := strconv.ParseUint(cfg.Server.UnixSocketMode, 8, 32)
		if err != nil {
			return nil, fmt.Errorf("failed to parse UNIX socket mode '%s': %w", cfg.Server.UnixSocketMode, err)
		}

		if err := os.Chmod(addr, os.FileMode(mode)); err != nil {
			return nil, fmt.Errorf("failed to set UNIX socket mode '%s': %w", cfg.Server.UnixSocketMode, err)
		}
	}

	// Service runner function
	runFn := func(ctx context.Context) error {

		// Start the server in a background goroutine
		go func() {
			defer listener.Close()

			slog.Info("Server listening", slog.String("addr", addr))
			srvErr := srv.Serve(listener)
			if srvErr != http.ErrServerClosed {
				slog.Error("Error starting app server", "error", srvErr)
				os.Exit(1)
			}
		}()

		// Notify systemd that we are ready
		err = systemd.SdNotifyReady()
		if err != nil {
			// Log the error only
			slog.Warn("Unable to notify systemd that the service is ready", "error", err)
		}

		// Block until the context is canceled
		<-ctx.Done()

		// Handle graceful shutdown
		// Note we use the background context here as ctx has been canceled already
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := srv.Shutdown(shutdownCtx) //nolint:contextcheck
		shutdownCancel()
		if shutdownErr != nil {
			// Log the error only (could be context canceled)
			slog.Warn("App server shutdown error", "error", shutdownErr)
		}

		return nil
	}

	return runFn, nil
}

func initLogger(r *gin.Engine) {
	loggerSkipPathsPrefix := []string{
		"GET /api/application-images/logo",
		"GET /api/application-images/background",
		"GET /api/application-images/favicon",
		"GET /_app",
		"GET /fonts",
		"GET /healthz",
		"HEAD /healthz",
	}

	r.Use(sloggin.SetLogger(
		sloggin.WithLogger(func(_ *gin.Context, _ *slog.Logger) *slog.Logger {
			return slog.Default()
		}),
		sloggin.WithSkipper(func(c *gin.Context) bool {
			for _, prefix := range loggerSkipPathsPrefix {
				if strings.HasPrefix(c.Request.Method+" "+c.Request.URL.String(), prefix) {
					return true
				}
			}
			return false
		}),
	))
}
