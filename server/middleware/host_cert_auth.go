package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// HostnameContextKey is the gin.Context key HostCertAuthMiddleware sets the
// authenticated hostname under once implemented.
const HostnameContextKey = "ssoossh.hostname"

// HostCertAuthMiddleware protects `host renew`: the caller authenticates by
// presenting its existing, still-valid host certificate rather than a
// fresh OIDC approval (hosts rotate keys on their own schedule — see
// docs/ssoossh-context.md, "Certificate types").
type HostCertAuthMiddleware struct{}

// NewHostCertAuthMiddleware creates a HostCertAuthMiddleware.
func NewHostCertAuthMiddleware() *HostCertAuthMiddleware {
	return &HostCertAuthMiddleware{}
}

// Add returns a gin.HandlerFunc that will validate a presented host
// certificate against the CA (signature, validity window, principals) and
// set HostnameContextKey.
//
// TODO: not implemented yet — fails closed with 501 rather than silently
// allowing every request through, per server/CLAUDE.md's Security-Critical
// Code Rules (explicit authorization checks before data access). Decide the
// transport for the presented certificate (custom header vs. mTLS) before
// implementing.
func (m *HostCertAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		_ = c.Error(&errorresponses.NotImplementedError{}) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		c.Abort()
	}
}
