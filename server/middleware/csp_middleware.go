package middleware

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/gin-gonic/gin"
)

// CspMiddleware sets a Content Security Policy header and, when possible,
// includes a per-request nonce for inline scripts.
type CspMiddleware struct{}

// NewCspMiddleware creates a CspMiddleware.
func NewCspMiddleware() *CspMiddleware { return &CspMiddleware{} }

// GetCSPNonce returns the CSP nonce generated for this request, if any.
func GetCSPNonce(c *gin.Context) string {
	if v, ok := c.Get("csp_nonce"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Add returns a gin.HandlerFunc that generates a per-request nonce, stores
// it on the context under "csp_nonce" for GetCSPNonce to retrieve, and sets
// a Content-Security-Policy header that allows scripts only via that nonce.
func (m *CspMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Generate a random base64 nonce for this request
		nonce := generateNonce()
		c.Set("csp_nonce", nonce)

		csp := "default-src 'self'; " +
			"base-uri 'self'; " +
			"object-src 'none'; " +
			"frame-ancestors 'none'; " +
			"form-action 'self'; " +
			"img-src 'self'; " +
			"font-src 'self'; " +
			"style-src 'self' 'unsafe-inline'; " +
			"script-src 'self' 'nonce-" + nonce + "'"

		c.Writer.Header().Set("Content-Security-Policy", csp)
		c.Next()
	}
}

// generateNonce returns a random base64url-encoded 16-byte nonce.
//
// not covered: the error branch below is defensive. As of Go 1.24,
// crypto/rand.Read never returns an error. It crashes the process instead
// if the underlying OS random source fails, so this path cannot be
// exercised or naturally reached.
func generateNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "" // if generation fails, return empty; policy will omit nonce
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
