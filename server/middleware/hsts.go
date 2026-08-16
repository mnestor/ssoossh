package middleware

import "github.com/gin-gonic/gin"

// HstsMiddleware sets the Strict-Transport-Security header on responses,
// telling browsers to reach this host only over HTTPS. Safe to register
// even when this process doesn't terminate TLS itself (e.g. behind a
// reverse proxy): browsers ignore the header on a connection they see as
// plain HTTP (RFC 6797), so sending it unconditionally is harmless, and
// some deployments require it present on the HTTP response regardless.
type HstsMiddleware struct {
	headerValue string
}

// NewHstsMiddleware creates an HstsMiddleware that sends headerValue as the
// Strict-Transport-Security header, e.g. "max-age=31536000; includeSubDomains".
func NewHstsMiddleware(headerValue string) *HstsMiddleware {
	return &HstsMiddleware{headerValue: headerValue}
}

// Add returns a gin.HandlerFunc that sets the Strict-Transport-Security
// header on each response; an empty headerValue disables the header.
func (m *HstsMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		if m.headerValue != "" {
			c.Header("Strict-Transport-Security", m.headerValue)
		}

		c.Next()
	}
}
