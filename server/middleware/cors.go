package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CorsMiddleware adds permissive CORS headers to the small set of routes
// that need to be callable cross-origin (see isCorsPath).
type CorsMiddleware struct{}

// NewCorsMiddleware creates a CorsMiddleware.
func NewCorsMiddleware() *CorsMiddleware {
	return &CorsMiddleware{}
}

// Add returns a gin.HandlerFunc that sets CORS headers and short-circuits
// preflight OPTIONS requests for paths matched by isCorsPath, and otherwise
// passes the request through unchanged.
func (m *CorsMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.FullPath()
		if path == "" {
			// The router doesn't map preflight requests, so we need to use the raw URL path
			path = c.Request.URL.Path
		}

		if !isCorsPath(path) {
			c.Next()
			return
		}

		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Authorization")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST")

		// Preflight request
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// isCorsPath reports whether path is one of the OIDC/well-known endpoints
// that need to be reachable cross-origin.
func isCorsPath(path string) bool {
	switch path {
	case "/api/oidc/token",
		"/api/oidc/userinfo",
		"/oidc/end-session",
		"/api/oidc/introspect",
		"/.well-known/jwks.json",
		"/.well-known/openid-configuration":
		return true
	default:
		return false
	}
}
