package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// adminLoginWindow is how far back the directory sync looks for a login when
// deciding whether a user it disables counts as an admin. Someone who has
// not logged in for longer is not told, whatever they once held.
const adminLoginWindow = 60 * 24 * time.Hour

// loginPayloadPrefix matches the stored payload of an auth.login event and
// nothing else. The action is the payload's first key (AuditEvent declares
// it first), and the closing quote and comma keep auth.login_denied out. A
// prefix LIKE reads the same on SQLite and Postgres, where the payload is
// TEXT on both; TestLoginPayloadPrefix_shouldMatchARecordedLogin fails if
// the encoding ever moves the action.
const loginPayloadPrefix = `{"action":"` + string(AuditAuthLogin) + `",%`

// syncAdmins answers, for one sync pass, which users count as admins for
// the auto-disable email. It is loaded on the first question, so a pass
// that disables nobody reads nothing, and a pass that disables many reads
// the audit log once rather than once per user.
//
// The answer only decides who is told. It never grants anything: authority
// comes from the session identity, and nothing here is read by a request.
type syncAdmins struct {
	loaded bool
	// groups is each user's group snapshot from their most recent
	// auth.login inside the window, keyed by users.id.
	groups map[string][]string
	// failed records a load that errored, so the pass does not retry the
	// same failing query for every user it disables.
	failed bool
}

// isSyncAdmin reports whether userID's most recent login inside the window
// carried a group the current config maps to admin. Reading the mapping
// from the config at the time of the pass, not at the time of the login,
// means a group that has since stopped granting admin no longer counts.
//
// A failed read is logged and answers false: the disable it accompanies
// still stands, and an email nobody can justify is not sent.
func (s *LDAPService) isSyncAdmin(ctx context.Context, admins *syncAdmins, userID string) bool {
	if !admins.loaded {
		admins.loaded = true
		now := time.Now()
		s.logShortAdminWindow(ctx, now)
		groups, err := latestLoginGroups(ctx, s.db, now.Add(-adminLoginWindow))
		if err != nil {
			admins.failed = true
			s.log.ErrorContext(ctx, "directory sync could not read recent logins, so no disabled user is treated as an admin for the auto-disable email",
				"error", err)
		}
		admins.groups = groups
	}
	if admins.failed {
		return false
	}
	groups, ok := admins.groups[userID]
	return ok && s.config.Admin.GrantsAdmin(groups)
}

// latestLoginGroups returns, per user, the groups their most recent
// auth.login event at or after since carried. One query for every user,
// whatever the population: the inner select finds each user's latest login
// time and the join reads that login's payload.
//
// created_at is written in UTC on both backends (see package dbtime), so
// the SQLite text comparison and MAX agree with Postgres' instants, and
// since is bound in UTC for the same reason. Two logins sharing a user's
// latest timestamp are resolved by id, so the answer does not depend on row
// order.
func latestLoginGroups(ctx context.Context, db *gorm.DB, since time.Time) (map[string][]string, error) {
	latest := db.Model(&model.AuditEvent{}).
		Select("actor_user_id, MAX(created_at) AS latest_at").
		Where("actor_user_id IS NOT NULL").
		Where("payload LIKE ?", loginPayloadPrefix).
		Where("created_at >= ?", since.UTC()).
		Group("actor_user_id")

	var rows []model.AuditEvent
	err := db.WithContext(ctx).
		Table("audit_events AS a").
		Select("a.id, a.created_at, a.actor_user_id, a.payload").
		Joins("JOIN (?) AS latest ON latest.actor_user_id = a.actor_user_id AND latest.latest_at = a.created_at", latest).
		Where("a.payload LIKE ?", loginPayloadPrefix).
		Order("a.id DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to read the latest login of each user: %w", err)
	}

	groups := make(map[string][]string, len(rows))
	for _, row := range rows {
		if row.ActorUserID == nil {
			// not covered: the inner select excludes NULL actors and the
			// join matches on equality, so a joined row always has one.
			continue
		}
		if _, seen := groups[*row.ActorUserID]; seen {
			continue
		}
		var login struct {
			Actor *AuditSubject `json:"actor"`
		}
		if err := json.Unmarshal([]byte(row.Payload), &login); err != nil || login.Actor == nil {
			// A login that cannot be read is no evidence of a role. The
			// user is left out, which is the side that sends no email.
			continue
		}
		groups[*row.ActorUserID] = login.Actor.Groups
	}
	return groups, nil
}

// logShortAdminWindow says so when the audit log cannot reach back the full
// adminLoginWindow: audit.retention below it, or a table whose oldest row is
// younger (audit.max_rows trimmed it, or the log is new). An admin whose
// last login fell outside what the log holds is not recognised, and the
// operator reading why someone was not emailed needs to see that here.
func (s *LDAPService) logShortAdminWindow(ctx context.Context, now time.Time) {
	window, limit := adminLoginWindow, ""
	if retention := s.config.Audit.Retention; retention > 0 && retention < window {
		window, limit = retention, "audit.retention"
	}

	var oldest model.AuditEvent
	err := s.db.WithContext(ctx).Select("created_at").Order("created_at ASC").First(&oldest).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		window, limit = 0, "the audit log is empty"
	case err != nil:
		s.log.WarnContext(ctx, "directory sync could not read the age of the audit log", "error", err)
	default:
		if held := now.Sub(oldest.CreatedAt); held < window {
			window, limit = held, "the audit log holds nothing older (audit.max_rows, or a new log)"
		}
	}

	if window < adminLoginWindow {
		s.log.InfoContext(ctx, "directory sync recognises admins only by logins the audit log still holds, which is less than its 60-day window",
			"effective_window", window.Round(time.Minute).String(), "limited_by", limit)
	}
}

// emailAutoDisabledAdmin tells an admin the sync has just disabled their
// account. Always sent: the kind is not a preference, so no choice the
// admin made silences it. Mail being switched off is the one thing that
// does, and it is logged rather than treated as a failure, since the
// disable it accompanies has already happened.
func (s *LDAPService) emailAutoDisabledAdmin(ctx context.Context, user *model.User, row *model.UserLDAP, missingSince time.Time, missingFor time.Duration, now time.Time) {
	if !s.config.Mail.Enabled {
		s.log.WarnContext(ctx, "mail is disabled (mail.enabled is false), so the admin the directory sync disabled was not emailed",
			"user_id", user.ID, "username", user.Username)
		return
	}
	if user.Email == "" {
		s.log.WarnContext(ctx, "the admin the directory sync disabled has no email address, so they were not emailed",
			"user_id", user.ID, "username", user.Username)
		return
	}

	support, _ := s.config.SupportContact()
	s.notifier.Notify(ctx, notify.KindAdminAutoDisabled, user.ID, &notify.AdminAutoDisabled{
		Username:     user.Username,
		DisabledAt:   now,
		MissingSince: missingSince,
		MissingFor:   missingFor.Round(time.Second),
		DisableAfter: s.config.LDAP.Sync.DisableAfter,
		DirectoryDN:  row.DN,
		BaseDN:       s.config.LDAP.BaseDN,
		Reenable:     s.config.LDAP.Sync.Reenable,
		SupportEmail: support,
		ServerURL:    s.config.HTTP.PublicOrigin(),
	})
	s.log.InfoContext(ctx, "queued the email telling an admin the directory sync disabled them",
		"user_id", user.ID, "username", user.Username)
}

// SetNotifier attaches the notification publisher, on the same terms as
// CertRequestService.SetNotifier: optional, wired at startup, and a no-op
// discard until then. Safe on a nil service, which is a disabled directory.
func (s *LDAPService) SetNotifier(n Notifier) {
	if s != nil && n != nil {
		s.notifier = n
	}
}
