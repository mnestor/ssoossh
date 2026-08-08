package middleware

import "github.com/gin-gonic/gin"

// HstsMiddleware sets the Strict-Transport-Security header on responses,
// telling browsers to reach this host only over HTTPS. Register it only on
// a server that terminates TLS itself: browsers ignore HSTS on plain-HTTP
// responses (RFC 6797).
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
