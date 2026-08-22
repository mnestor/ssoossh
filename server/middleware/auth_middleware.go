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

// AuditorAuthMiddleware ensures the caller holds auditor-level access. Auditor
// is a child role of admin: membership in the admin group satisfies it, as does
// membership in the configured auditor group. Fails closed: no identity, or
// membership in neither group, denies with 403 Forbidden.
type AuditorAuthMiddleware struct {
	config *config.Config
}

// NewAuditorAuthMiddleware creates an AuditorAuthMiddleware.
func NewAuditorAuthMiddleware(c *config.Config) *AuditorAuthMiddleware {
	return &AuditorAuthMiddleware{config: c}
}

// Add returns a gin.HandlerFunc that authorizes auditor-scoped routes. Admins
// are a superset of auditors: admin group membership grants access even when
// the auditor group is unconfigured. Otherwise auditor access requires the
// auditor group to be configured and the caller to belong to it. Fails closed
// with 403 Forbidden when neither path grants access.
func (m *AuditorAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := Identity(c)
		if !ok || identity == nil {
			_ = c.Error(&ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		// Admin implies auditor. Checked independently of the auditor group so
		// that leaving auditor_group unset narrows access to admins rather than
		// locking everyone out.
		if m.config.Admin.IsAdminEnabled() &&
			containsString(identity.Groups, m.config.Admin.RequireGroup) {
			c.Next()
			return
		}

		if !m.config.Admin.IsAuditorEnabled() {
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

// containsString reports whether needle is in haystack. An empty needle never
// matches, so an unconfigured group cannot accidentally authorize a caller.
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
