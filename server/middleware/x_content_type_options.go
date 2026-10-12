package middleware

import "github.com/gin-gonic/gin"

// XContentTypeOptionsMiddleware sets the X-Content-Type-Options header on
// responses, telling browsers not to MIME-sniff the content type and to use
// the Content-Type header instead. This mitigates XSS attacks via
// MIME-type confusion.
type XContentTypeOptionsMiddleware struct{}

// xContentTypeOptions is the X-Content-Type-Options value sent on every
// response. It is also reported to the admin diagnostics through
// AppSecurityHeaders.
const xContentTypeOptions = "nosniff"

// NewXContentTypeOptionsMiddleware creates an XContentTypeOptionsMiddleware.
func NewXContentTypeOptionsMiddleware() *XContentTypeOptionsMiddleware {
	return &XContentTypeOptionsMiddleware{}
}

// Add returns a gin.HandlerFunc that sets the X-Content-Type-Options header
// to nosniff on each response.
func (m *XContentTypeOptionsMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", xContentTypeOptions)
		c.Next()
	}
}
