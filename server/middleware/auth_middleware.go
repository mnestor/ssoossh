package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/service"
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
			_ = c.Error(&ForbiddenError{})
			c.Abort()
			return
		}

		identity, ok := Identity(c)
		if !ok || identity == nil {
			_ = c.Error(&ForbiddenError{})
			c.Abort()
			return
		}

		if !containsString(identity.Groups, m.config.Admin.RequireGroup) {
			_ = c.Error(&ForbiddenError{})
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
			_ = c.Error(&ForbiddenError{})
			c.Abort()
			return
		}

		identity, ok := Identity(c)
		if !ok || identity == nil {
			_ = c.Error(&ForbiddenError{})
			c.Abort()
			return
		}

		if !containsString(identity.Groups, m.config.Admin.AuditorGroup) {
			_ = c.Error(&ForbiddenError{})
			c.Abort()
			return
		}

		c.Next()
	}
}

// SSHServerAdminChecker determines whether a caller is authorized to manage
// SSH host certificates. Consumed by feat/host-certs via the interface
// contract.
type SSHServerAdminChecker interface {
	// IsSSHServerAdmin reports whether the caller identified by id is
	// authorized to manage SSH host certificates.
	IsSSHServerAdmin(id *service.Identity) bool
}

// ConfigSSHServerAdminChecker is the production implementation of
// SSHServerAdminChecker, checking group membership against config.
type ConfigSSHServerAdminChecker struct {
	config *config.Config
}

// NewConfigSSHServerAdminChecker creates a ConfigSSHServerAdminChecker.
func NewConfigSSHServerAdminChecker(c *config.Config) *ConfigSSHServerAdminChecker {
	return &ConfigSSHServerAdminChecker{config: c}
}

// IsSSHServerAdmin reports whether id belongs to the configured SSH server
// admin group. Fails closed: no configured group, no groups on the identity,
// or no membership all return false.
func (c *ConfigSSHServerAdminChecker) IsSSHServerAdmin(id *service.Identity) bool {
	if !c.config.Admin.IsSSHServerAdminEnabled() {
		return false
	}
	if id == nil {
		return false
	}
	return containsString(id.Groups, c.config.Admin.SSHServerAdminGroup)
}

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
