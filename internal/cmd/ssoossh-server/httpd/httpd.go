// Created by Mike Nestor <me@mikenestor.org>
package httpd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/glebarez/sqlite"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	slogchi "github.com/samber/slog-chi"
	"gorm.io/gorm"

	apiV1 "github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/api/v1"
	mware "github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware/auth"
	"github.com/mnestor/ssoossh/internal/config"
	"github.com/mnestor/ssoossh/internal/log"
	"github.com/mnestor/ssoossh/internal/store"
	"github.com/mnestor/ssoossh/internal/version"
)

type HttpServer struct {
	*chi.Mux
	SessionManager   *mware.SessionManager
	CertificateStore store.CertificateInterface
	CertRequestStore store.CertRequestInterface
	AuditLogStore    store.AuditLogInterface
}

func NewServer() (*HttpServer, error) {

	c := config.GetConfig()
	router := chi.NewRouter()

	if c.Server.AccessLog {
		slogSettings := slogchi.Config{
			WithRequestHeader:  true,
			WithResponseHeader: false,
			WithSpanID:         false,
			WithTraceID:        false,
			WithUserAgent:      false,
			WithRequestID:      false,
		}

		router.Use(slogchi.NewWithConfig(log.GetLogger(), slogSettings))
	}

	router.Use(middleware.NoCache)
	router.Use(mware.Hsts)

	router.Use(httprate.LimitByRealIP(c.Server.RateLimit, c.Server.RateDuration))
	router.Use(mware.Sni)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)

	sessionManager := mware.NewSessionManager()
	router.Use(sessionManager.RegisterHandler)
	router.Use(sessionManager.LoadAndSave)

	certStore := store.NewMemoryCertificatesStore()
	reqCertStore := store.NewMemoryCertRequestStore()

	db, err := gorm.Open(sqlite.Open(c.Server.DatabasePath), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	auditStore, err := store.NewGormAuditLogStore(db)
	if err != nil {
		return nil, fmt.Errorf("failed to init audit log store: %w", err)
	}

	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = context.WithValue(ctx, store.CertRequestContext, store.CertRequestInterface(reqCertStore))
			ctx = context.WithValue(ctx, store.CertificateContext, store.CertificateInterface(certStore))
			ctx = context.WithValue(ctx, store.AuditLogContext, store.AuditLogInterface(auditStore))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	// route permission enforcement
	enforcer := auth.New()
	router.Use(enforcer.CheckAccessHandler)
	router.Use(enforcer.RegisterHandler)

	// do this before other routes to setup oauth
	auth.SetupRoutes(router)

	router.Mount("/api/v1", apiV1.NewRouter())
	router.Get("/ca", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fmt.Sprintf("/api/%s/ca", version.ApiPath), http.StatusFound)
	})

	router.Get("/approve/{id}", apiGetApprove)
	router.Get("/reject/{id}", apiGetReject)

	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Hello World!"))
	})

	return &HttpServer{
		router,
		sessionManager,
		certStore,
		reqCertStore,
		auditStore,
	}, nil
}

func (s *HttpServer) Listen() error {
	c := config.GetConfig().Server
	listen := fmt.Sprintf(
		"%s:%d",
		c.Address,
		c.Port,
	)

	defer s.Close()

	slog.Info("Webserver is now listening for connections", "Address", listen)
	if c.Port == 80 {
		server := &http.Server{Addr: listen, Handler: s}
		return server.ListenAndServe()
	}

	server := &http.Server{
		Addr:    listen,
		Handler: s,
		TLSConfig: &tls.Config{
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				ct := config.GetConfig().Server.Tls
				var cert tls.Certificate
				var err error
				if ct.KeyFile != "" {
					cert, err = tls.LoadX509KeyPair(ct.CertFile, ct.KeyFile)
				} else if ct.Key != "" {
					cert, err = tls.X509KeyPair([]byte(c.Tls.Cert), []byte(c.Tls.Key))
				}
				return &cert, err
			},
		},
	}
	return server.ListenAndServeTLS("", "")
}

func (s *HttpServer) Close() {
}
