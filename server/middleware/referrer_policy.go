package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/secheaders"
)

// ReferrerPolicyMiddleware sets the Referrer-Policy header on responses,
// controlling when the Referer header is sent to other origins. The
// strict-origin-when-cross-origin policy sends the full Referer URL only for
// same-origin requests, and only the origin for cross-origin requests.
type ReferrerPolicyMiddleware struct{}

// NewReferrerPolicyMiddleware creates a ReferrerPolicyMiddleware.
func NewReferrerPolicyMiddleware() *ReferrerPolicyMiddleware {
	return &ReferrerPolicyMiddleware{}
}

// Add returns a gin.HandlerFunc that sets the Referrer-Policy header to
// secheaders.ReferrerPolicy on each response.
func (m *ReferrerPolicyMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Referrer-Policy", secheaders.ReferrerPolicy)
		c.Next()
	}
}
