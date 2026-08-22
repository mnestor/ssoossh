package service

import (
	"slices"

	"github.com/mnestor/ssoossh/server/config"
)

// SSHServerAdminChecker verifies if an identity has SSH server admin
// privileges. This interface is defined here to match feat/auth-roles'
// contract before that feature lands; the implementation will be replaced
// when feat/auth-roles merges.
type SSHServerAdminChecker interface {
	// IsSSHServerAdmin returns true if identity has SSH server admin
	// privileges, false otherwise. Returns false when the configured group
	// is empty, failing closed by default.
	IsSSHServerAdmin(identity *Identity) bool
}

// configSSHServerAdminChecker is the default implementation of
// SSHServerAdminChecker, checking membership in a configured LDAP group.
type configSSHServerAdminChecker struct {
	requiredGroup string
}

// NewConfigSSHServerAdminChecker creates an SSHServerAdminChecker that
// checks membership in the configured SSH server admin group.
func NewConfigSSHServerAdminChecker(c *config.Config) SSHServerAdminChecker {
	return &configSSHServerAdminChecker{
		requiredGroup: c.SSHServerAdminGroup,
	}
}

// IsSSHServerAdmin checks if the given identity is a member of the
// configured SSH server admin group.
func (ch *configSSHServerAdminChecker) IsSSHServerAdmin(identity *Identity) bool {
	// Fail closed: if no group is configured, no one is an admin.
	if ch.requiredGroup == "" {
		return false
	}
	return slices.Contains(identity.Groups, ch.requiredGroup)
}
