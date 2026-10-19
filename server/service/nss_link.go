package service

import (
	"context"
	"errors"
	"strconv"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// linkLogin links the account a person's directory entry carries to their
// users row, at login, once the LDAP enrichment has resolved the entry
// (LDAPService.Enrich calls it on the same connection). The account must be
// that same directory entry: found by directory_id, then DN. When none is
// stored yet (an entry created since the last sync) the entry's uid is read
// on demand, exactly as a host's lookup would read it, and the result is
// linked only if it is the same entry. A row that merely carries the same
// uid is another entry (a uid the directory hands out twice, one copy
// outside nss.user_filter) and is never linked: the person would control
// its shell, and their disable would lock it.
//
// A row already linked to this person is left alone; one linked to
// someone else is never moved silently (a warning, and it stays), and
// neither is a second row for a person who already has one. When the
// person's username differs from the account's name, nss.name_mismatch is
// raised at most daily: their own-name certificate principal can never
// resolve on a host.
//
// Nothing here fails or holds up the login beyond the reads themselves:
// every error is logged and the login goes on.
func (s *NSSDirectoryService) linkLogin(ctx context.Context, conn ldapConn, identity *Identity, userID string, entry *ldapEntry) {
	if s == nil || entry == nil || userID == "" {
		return
	}
	logAttrs := []any{"user_id", userID, "dn", entry.DN}

	raw := entry.Attrs[s.config.NSS.Attributes.UID]
	if raw == "" {
		s.log.DebugContext(ctx, "the person's directory entry has no uid attribute; no NSS account to link", logAttrs...)
		return
	}
	uid64, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		s.log.WarnContext(ctx, "the person's directory entry has a uid that is not a number; no NSS account linked",
			append(logAttrs, "uid", raw)...)
		return
	}
	uid := uint32(uid64)

	row, err := s.loginRow(ctx, entry)
	if err == nil && row == nil {
		filter := s.andFilter(s.config.NSS.UserFilter, s.config.NSS.Attributes.UID, raw)
		if _, _, err = s.readAccountsOnDemand(ctx, conn, filter, nssKinds{number: true}, nssViaLogin, func(a *nssAccount) bool { return a.UID == uid }); err == nil {
			row, err = s.loginRow(ctx, entry)
		}
	}
	if err != nil {
		s.log.WarnContext(ctx, "could not link the person's NSS account; the login goes on", append(logAttrs, "error", err)...)
		return
	}
	if row == nil {
		s.log.DebugContext(ctx, "the person's directory entry is not an NSS account; nothing linked",
			append(logAttrs, "uid", uid)...)
		return
	}

	linked, err := s.link(ctx, row, userID)
	if err != nil {
		s.log.WarnContext(ctx, "could not link the person's NSS account; the login goes on", append(logAttrs, "error", err)...)
		return
	}
	if linked && identity != nil && identity.Username != row.Name {
		s.raiseNameMismatch(ctx, AuditSubjectFromIdentity(identity, userID), row)
	}
}

// loginRow finds the stored account that is a person's entry: by
// directory_id, then DN, never by uid alone (see linkLogin). Nil when there
// is none.
func (s *NSSDirectoryService) loginRow(ctx context.Context, entry *ldapEntry) (*model.PosixUser, error) {
	db := s.db.WithContext(ctx)
	anchors := []struct {
		where string
		value any
		use   bool
	}{
		{"directory_id = ?", entry.DirectoryID, entry.DirectoryID != ""},
		{"dn = ?", entry.DN, entry.DN != ""},
	}
	for _, a := range anchors {
		if !a.use {
			continue
		}
		var row model.PosixUser
		err := db.Where(a.where, a.value).Order("created_at").First(&row).Error
		if err == nil {
			return &row, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	return nil, nil //nolint:nilnil // no stored account is an answer, not an error.
}

// link sets row's user_id to userID unless it is set already, reporting
// whether the row is now linked to userID. A row linked to someone else,
// or a person already linked to a different row, is left as it is with a
// warning: moving a link would hand one person's files to another.
func (s *NSSDirectoryService) link(ctx context.Context, row *model.PosixUser, userID string) (bool, error) {
	logAttrs := []any{"user_id", userID, "name", row.Name, "uid", row.UID, "dn", row.DN}
	if row.UserID != nil {
		if *row.UserID == userID {
			return true, nil
		}
		s.log.WarnContext(ctx, "an NSS account is already linked to another person; the link was left as it is",
			append(logAttrs, "linked_user_id", *row.UserID)...)
		return false, nil
	}

	db := s.db.WithContext(ctx)
	var other model.PosixUser
	err := db.Where("user_id = ? AND id <> ?", userID, row.ID).First(&other).Error
	switch {
	case err == nil:
		s.log.WarnContext(ctx, "the person is already linked to another NSS account; the new one was not linked",
			append(logAttrs, "linked_name", other.Name, "linked_uid", other.UID)...)
		return false, nil
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return false, err
	}

	result := db.Model(&model.PosixUser{}).
		Where("id = ? AND user_id IS NULL", row.ID).
		Updates(map[string]any{"user_id": userID, "updated_at": s.now().UTC()})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		// Another login linked it between the read and the write.
		return false, nil
	}
	s.log.InfoContext(ctx, "linked an NSS account to the person who logged in with it", logAttrs...)
	return true, nil
}
