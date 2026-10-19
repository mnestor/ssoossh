package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
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
// closed with 403 Forbidden if the caller is not an admin. An unconfigured
// admin group denies too: config.AdminConfig.GrantsAdmin is the single
// authority for the rule and never grants without a configured group.
func (m *AdminAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := Identity(c)
		if !ok || identity == nil {
			_ = c.Error(&errorresponses.ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		if !m.config.Admin.GrantsAdmin(identity.Groups) {
			_ = c.Error(&errorresponses.ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		c.Next()
	}
}

// SOCAuthMiddleware ensures the caller holds SOC-level access, which guards
// the containment operations (disabling users, expiring enrollments). SOC is
// a child role of admin: membership in the admin group satisfies it, as does
// membership in the configured SOC group. Fails closed: no identity, or
// membership in neither group, denies with 403 Forbidden.
type SOCAuthMiddleware struct {
	config *config.Config
}

// NewSOCAuthMiddleware creates a SOCAuthMiddleware.
func NewSOCAuthMiddleware(c *config.Config) *SOCAuthMiddleware {
	return &SOCAuthMiddleware{config: c}
}

// Add returns a gin.HandlerFunc that authorizes SOC-scoped routes. Admins are
// a superset of SOC: admin group membership grants access even when the SOC
// group is unconfigured. Otherwise SOC access requires the SOC group to be
// configured and the caller to belong to it. Fails closed with 403 Forbidden
// when neither path grants access.
func (m *SOCAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := Identity(c)
		if !ok || identity == nil {
			_ = c.Error(&errorresponses.ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		if !m.config.Admin.GrantsSOC(identity.Groups) {
			_ = c.Error(&errorresponses.ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		c.Next()
	}
}

// AuditorAuthMiddleware ensures the caller holds auditor-level access. Auditor
// is a child role of admin and SOC: membership in either of those groups
// satisfies it, as does membership in the configured auditor group. Fails
// closed: no identity, or membership in none of the groups, denies with 403
// Forbidden.
type AuditorAuthMiddleware struct {
	config *config.Config
}

// NewAuditorAuthMiddleware creates an AuditorAuthMiddleware.
func NewAuditorAuthMiddleware(c *config.Config) *AuditorAuthMiddleware {
	return &AuditorAuthMiddleware{config: c}
}

// Add returns a gin.HandlerFunc that authorizes auditor-scoped routes. Admins
// and SOC members are supersets of auditors: membership in either group
// grants access even when the auditor group is unconfigured. Otherwise
// auditor access requires the auditor group to be configured and the caller
// to belong to it. Fails closed with 403 Forbidden when no path grants
// access.
func (m *AuditorAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := Identity(c)
		if !ok || identity == nil {
			_ = c.Error(&errorresponses.ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		if !m.config.Admin.GrantsAuditor(identity.Groups) {
			_ = c.Error(&errorresponses.ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		c.Next()
	}
}

// NSSKeyListAuthMiddleware guards the one admin route a fleet key admin
// reads without an admin area role: the list of every fleet key (GET
// /api/admin/nss/keys). It admits an auditor (and so SOC and admins) or a
// fleet key admin (admin.nss_key_group, which admin implies). Every other
// /api/admin route keeps its own role middleware, so this widens nothing
// else. Fails closed: no identity, or neither role, denies with 403
// Forbidden.
type NSSKeyListAuthMiddleware struct {
	config *config.Config
}

// NewNSSKeyListAuthMiddleware creates an NSSKeyListAuthMiddleware.
func NewNSSKeyListAuthMiddleware(c *config.Config) *NSSKeyListAuthMiddleware {
	return &NSSKeyListAuthMiddleware{config: c}
}

// Add returns a gin.HandlerFunc that authorizes the fleet key list. Both
// rules are the config's own (GrantsAuditor, GrantsNSSKeyAdmin), so a
// change to either role reaches this route without a second copy here.
func (m *NSSKeyListAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := Identity(c)
		if !ok || identity == nil {
			_ = c.Error(&errorresponses.ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		if !m.config.Admin.GrantsAuditor(identity.Groups) && !m.config.Admin.GrantsNSSKeyAdmin(identity.Groups) {
			_ = c.Error(&errorresponses.ForbiddenError{}) //nolint:errcheck
			c.Abort()
			return
		}

		c.Next()
	}
}
