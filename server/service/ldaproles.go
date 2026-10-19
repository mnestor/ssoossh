package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// Triggers recorded on a user.role_narrowed_by_directory event: which
// successful directory read wrote the rows that dropped the role group.
const (
	roleNarrowedAtLogin = "login"
	roleNarrowedBySync  = "directory-sync"
)

// RoleNarrower answers which of a user's groups still count once the
// directory has had its say (ldap.authoritative_role_groups). Implemented
// by *LDAPService; an interface so the notification service, which decides
// who the admin mail reaches, asks the same question a session does
// without holding the directory service itself.
type RoleNarrower interface {
	NarrowRoleGroups(ctx context.Context, userID string, groups []string) ([]string, error)
}

// NarrowRoleGroups is the one place ldap.authoritative_role_groups is
// applied. It returns groups without each role group (admin, SOC, auditor,
// fleet key admin) that the user's latest directory read no longer lists,
// and every other group untouched. It never adds a group: a role group the
// directory lists but groups does not carry stays absent.
//
// groups is the OIDC side: the session's claim for a live request or a
// login, the stored OIDC rows for the admin mail. userID is the users row.
//
// Nothing is narrowed when the setting is off, when LDAP is off (a nil
// service), when groups holds no role group, or when the user is not
// linked: no user_ldap row, or one with neither a DN nor a directory id.
// Only a successful directory read writes the rows read here (see
// LDAPService.persist), so a directory outage never narrows anyone.
//
// A database error is the one case that narrows without evidence: the
// answer then drops every role group, and the error is returned for the
// caller to log. A role the server could not check is not granted on
// trust, and the next request asks again.
func (s *LDAPService) NarrowRoleGroups(ctx context.Context, userID string, groups []string) ([]string, error) {
	if !s.authoritativeRoles() || userID == "" || !s.config.Admin.HoldsRoleGroup(groups) {
		return groups, nil
	}

	row, err := s.storedRow(ctx, userID)
	if err != nil {
		return s.config.Admin.NarrowRoleGroups(groups, nil), err
	}
	if !directoryLinked(row) {
		return groups, nil
	}

	var directory []string
	if err := s.db.WithContext(ctx).Model(&model.UserGroup{}).
		Where("user_id = ? AND source = ?", userID, model.GroupSourceLDAP).
		Pluck("group_name", &directory).Error; err != nil {
		return s.config.Admin.NarrowRoleGroups(groups, nil), fmt.Errorf("failed to read the directory groups of user %s: %w", userID, err)
	}
	return s.config.Admin.NarrowRoleGroups(groups, directory), nil
}

// dropRoleGroups removes every role group from groups when
// ldap.authoritative_role_groups is on, for a request whose user could not
// even be looked up. With the setting off it returns groups unchanged.
func (s *LDAPService) dropRoleGroups(groups []string) []string {
	if !s.authoritativeRoles() {
		return groups
	}
	return s.config.Admin.NarrowRoleGroups(groups, nil)
}

// authoritativeRoles reports whether ldap.authoritative_role_groups is in
// force. Safe on a nil service, which is LDAP switched off.
func (s *LDAPService) authoritativeRoles() bool {
	return s != nil && s.config.LDAP.AuthoritativeRoleGroups
}

// directoryLinked reports whether row ties a user to a directory entry the
// server resolved. Every row persist writes carries a DN; the directory id
// is checked as well so a row is never judged unlinked by one empty column.
func directoryLinked(row *model.UserLDAP) bool {
	return row != nil && (row.DN != "" || row.DirectoryID != "")
}

// removedGroups lists the members of before that after no longer holds, in
// before's order.
func removedGroups(before, after []string) []string {
	var out []string
	for _, g := range before {
		if !slices.Contains(after, g) && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// roleNarrowedEvent builds the audit event for a directory read that took
// role groups away from a user who holds them in their OIDC rows, or nil
// when it took none. removed is what the read dropped from the user's LDAP
// rows; only role groups the user's last login still carried are a change
// anyone holds, so a group they never had through OIDC is no event.
//
// Read inside the persist transaction, so the event describes exactly the
// rows that commit with it.
func (s *LDAPService) roleNarrowedEvent(tx *gorm.DB, userID string, removed []string, trigger string, now time.Time) (*AuditEvent, error) {
	if !s.authoritativeRoles() {
		return nil, nil //nolint:nilnil // no narrowing, so no event, which is an answer rather than an error.
	}
	var roles []string
	for _, g := range removed {
		if s.config.Admin.IsRoleGroup(g) {
			roles = append(roles, g)
		}
	}
	if len(roles) == 0 {
		return nil, nil //nolint:nilnil // the read dropped no role group.
	}

	var held []string
	if err := tx.Model(&model.UserGroup{}).
		Where("user_id = ? AND source = ?", userID, model.GroupSourceOIDC).
		Pluck("group_name", &held).Error; err != nil {
		return nil, fmt.Errorf("failed to read the OIDC groups of user %s: %w", userID, err)
	}
	var lost []string
	for _, g := range roles {
		if slices.Contains(held, g) {
			lost = append(lost, g)
		}
	}
	if len(lost) == 0 {
		return nil, nil //nolint:nilnil // the dropped role groups were never held through OIDC.
	}

	var user model.User
	if err := tx.Select("id", "subject", "username", "email").First(&user, "id = ?", userID).Error; err != nil {
		return nil, fmt.Errorf("failed to read user %s for the role audit event: %w", userID, err)
	}
	return &AuditEvent{
		Action:     AuditUserRoleNarrowedByDirectory,
		System:     true,
		Target:     AuditSubjectFromUser(&user),
		Reason:     "the latest directory read no longer lists the role group (ldap.authoritative_role_groups)",
		OccurredAt: now,
		Detail: map[string]any{
			"groups":  lost,
			"trigger": trigger,
		},
	}, nil
}
