package middleware

import "github.com/gin-gonic/gin"

// CacheControlMiddleware sets a safe default Cache-Control header on responses
// that do not already specify one. This prevents proxies from caching
// authenticated responses that might contain private data.
type CacheControlMiddleware struct {
	headerValue string
}

// NewCacheControlMiddleware creates a CacheControlMiddleware that defaults
// responses to "private, no-store".
func NewCacheControlMiddleware() *CacheControlMiddleware {
	return &CacheControlMiddleware{
		headerValue: "private, no-store",
	}
}

// Add returns a gin.HandlerFunc that sets the default Cache-Control header
// on any response that doesn't already have one set.
func (m *CacheControlMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Writer.Header().Get("Cache-Control") == "" {
			c.Header("Cache-Control", m.headerValue)
		}

		c.Next()
	}
}
