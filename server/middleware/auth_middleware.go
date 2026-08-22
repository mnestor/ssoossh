package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
)

// AdminAuthMiddleware ensures the caller belongs to the configured admin
// group. Fails closed: no identity, no configured group, or no membership
// all deny with 403 Forbidden.
type AdminAuthMiddleware struct {
	config *config.Config
}

// NewAdminAuthMiddleware creates an AdminAuthMiddleware.
func NewAdminAuthMiddleware(c *config.Config) *AdminAuthMiddleware {
	return &AdminAuthMiddleware{config: c}
}

// Add returns a gin.HandlerFunc that checks admin group membership and fails
// closed with 403 Forbidden if the caller is not an admin.
func (m *AdminAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.config.Admin.IsAdminEnabled() {
			_ = c.Error(&ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		identity, ok := Identity(c)
		if !ok || identity == nil {
			_ = c.Error(&ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		if !containsString(identity.Groups, m.config.Admin.RequireGroup) {
			_ = c.Error(&ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		c.Next()
	}
}

// AuditorAuthMiddleware ensures the caller belongs to the configured auditor
// group. Fails closed: no identity, no configured group, or no membership
// all deny with 403 Forbidden.
type AuditorAuthMiddleware struct {
	config *config.Config
}

// NewAuditorAuthMiddleware creates an AuditorAuthMiddleware.
func NewAuditorAuthMiddleware(c *config.Config) *AuditorAuthMiddleware {
	return &AuditorAuthMiddleware{config: c}
}

// Add returns a gin.HandlerFunc that checks auditor group membership and
// fails closed with 403 Forbidden if the caller is not an auditor.
func (m *AuditorAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.config.Admin.IsAuditorEnabled() {
			_ = c.Error(&ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		identity, ok := Identity(c)
		if !ok || identity == nil {
			_ = c.Error(&ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		if !containsString(identity.Groups, m.config.Admin.AuditorGroup) {
			_ = c.Error(&ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		c.Next()
	}
}

// SSHServerAdminChecker determines whether a caller is authorized to manage
// SSH host certificates. Consumed by feat/host-certs via the interface
// contract.
// containsString reports whether needle is in haystack.
func containsString(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
