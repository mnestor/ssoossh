package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// PosixAccountProvider is the user controller's view of
// NSSLookupService: the caller's own POSIX account and their shell. An
// interface so the /users/me contract can be tested without a database.
type PosixAccountProvider interface {
	AccountForIdentity(ctx context.Context, identity *Identity) (*PosixAccount, error)
	SetShellPref(ctx context.Context, identity *Identity, pref *string, sourceIP string) (*PosixAccount, error)
}

// PosixAccount is a person's own POSIX account, as the account page shows
// it: the number and name hosts know them by and the shell they chose.
type PosixAccount struct {
	UID       uint32
	Name      string
	ShellPref *string
}

// AllowedShells returns nss.allowed_shells, the names a person may choose
// from. A copy, so a caller cannot edit the configuration.
func (s *NSSLookupService) AllowedShells() []string {
	return slices.Clone(s.config.NSS.AllowedShells)
}

// AccountForIdentity returns the POSIX account linked to the identity's
// users row, or nil when there is none: no users row, or no posix_users row
// carries its id (a person the login link has not matched yet).
func (s *NSSLookupService) AccountForIdentity(ctx context.Context, identity *Identity) (*PosixAccount, error) {
	userID, err := s.userID(ctx, identity)
	if err != nil || userID == "" {
		return nil, err
	}
	row, err := s.linkedRow(ctx, s.db, userID)
	if err != nil || row == nil {
		return nil, err
	}
	return &PosixAccount{UID: row.UID, Name: row.Name, ShellPref: row.ShellPref}, nil
}

// SetShellPref records the shell the identity chose for their own POSIX
// account, or clears it when pref is nil, and returns the account as
// stored. The name must be one of nss.allowed_shells: anything else is a
// 400 whose reason starts with shell_not_allowed. A session with no users
// row is refused as every user-scoped write is; a person with no linked
// account gets a 404. The change is audited with the value before and
// after; setting the value already stored writes nothing.
func (s *NSSLookupService) SetShellPref(ctx context.Context, identity *Identity, pref *string, sourceIP string) (*PosixAccount, error) {
	if pref != nil && !slices.Contains(s.config.NSS.AllowedShells, *pref) {
		return nil, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf(
			"shell_not_allowed: %q is not one of the shells this server allows (%s)", *pref, strings.Join(s.config.NSS.AllowedShells, ", "))}
	}
	userID, err := s.userID(ctx, identity)
	if err != nil {
		return nil, err
	}
	if userID == "" {
		return nil, &errorresponses.ForbiddenError{Reason: "no user record for the authenticated identity"}
	}

	var event *AuditEvent
	var account *PosixAccount
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := s.linkedRow(ctx, tx, userID)
		if err != nil {
			return err
		}
		if row == nil {
			return &errorresponses.NotFoundError{Resource: "a POSIX account linked to you"}
		}
		account = &PosixAccount{UID: row.UID, Name: row.Name, ShellPref: pref}
		if equalOptional(row.ShellPref, pref) {
			return nil
		}
		if err := tx.Model(&model.PosixUser{}).Where("id = ?", row.ID).
			Updates(map[string]any{"shell_pref": pref, "updated_at": s.now()}).Error; err != nil {
			return fmt.Errorf("save the shell preference: %w", err)
		}
		event = &AuditEvent{
			Action:  AuditNSSShellChanged,
			Actor:   AuditSubjectFromIdentity(identity, userID),
			NSSRefs: []NSSAuditRef{accountTable().auditRef(row.ID)},
			Detail: map[string]any{
				"uid":       row.UID,
				"name":      row.Name,
				"before":    optionalValue(row.ShellPref),
				"after":     optionalValue(pref),
				"source_ip": sourceIP,
			},
		}
		if s.auditor == nil {
			return nil
		}
		return s.auditor.RecordTx(tx, *event)
	})
	if err != nil {
		return nil, err
	}
	if event != nil && s.auditor != nil {
		s.auditor.LogOnly(*event)
	}
	return account, nil
}

// userID resolves the identity's users.id by subject, or "" when the
// session has outlived its row.
func (s *NSSLookupService) userID(ctx context.Context, identity *Identity) (string, error) {
	if identity == nil {
		// not covered: every caller comes from middleware.Identity, which
		// reports absence separately.
		return "", &errorresponses.UnauthorizedError{}
	}
	var user model.User
	err := s.db.WithContext(ctx).Select("id").Where("subject = ?", identity.Subject).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve the user record: %w", err)
	}
	return user.ID, nil
}

// linkedRow reads the posix_users row linked to userID through db, or nil.
func (s *NSSLookupService) linkedRow(ctx context.Context, db *gorm.DB, userID string) (*model.PosixUser, error) {
	var row model.PosixUser
	err := db.WithContext(ctx).Where("user_id = ?", userID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the linked posix account: %w", err)
	}
	return &row, nil
}

// equalOptional reports whether two optional strings hold the same value,
// both absent included.
func equalOptional(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// optionalValue renders an optional string for an audit detail: its value,
// or nil so the payload says null rather than an empty string.
func optionalValue(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
