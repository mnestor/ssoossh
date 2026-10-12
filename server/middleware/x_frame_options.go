package middleware

import "github.com/gin-gonic/gin"

// XFrameOptionsMiddleware sets the X-Frame-Options header on responses,
// telling browsers not to display this page in a frame, iframe, object, or
// embed element (DENY). This mitigates clickjacking attacks.
type XFrameOptionsMiddleware struct{}

// xFrameOptions is the X-Frame-Options value sent on every response. It is
// also reported to the admin diagnostics through AppSecurityHeaders.
const xFrameOptions = "DENY"

// NewXFrameOptionsMiddleware creates an XFrameOptionsMiddleware.
func NewXFrameOptionsMiddleware() *XFrameOptionsMiddleware {
	return &XFrameOptionsMiddleware{}
}

// Add returns a gin.HandlerFunc that sets the X-Frame-Options header to DENY
// on each response.
func (m *XFrameOptionsMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Frame-Options", xFrameOptions)
		c.Next()
	}
}
