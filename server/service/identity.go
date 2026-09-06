package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// IdentityRefresher re-reads the durable half of a session identity — the
// account lists and the extra fields — so a request is authorized against
// what the person holds now rather than what they held at login.
//
// Implemented by IdentityService. Consumed by
// middleware.SessionAuthMiddleware, which is the one place every
// session-authenticated request passes through.
type IdentityRefresher interface {
	RefreshIdentity(ctx context.Context, identity *Identity) error
}

// IdentityService answers "what does this identity hold right now" from the
// database, and is the single authority for that question.
//
// The split it reconciles: the users row carries the OIDC claim values,
// written at login before enrichment runs, and user_ldap.attributes carries
// the directory values, refreshed by the background sync. Neither alone is
// the answer, and the session cookie is neither — it is a snapshot of the
// merge taken at login and frozen there.
//
// That snapshot is what this exists to stop trusting. The directory sync can
// remove a shared account between logins, and until it was refreshed here a
// live session went on offering that account as a certificate principal:
// approval linkage reads identity.ServiceAccounts and identity.OtherAccounts,
// so a stale session was a stale authorization.
//
// Groups are deliberately not refreshed. Authorization roles are evaluated
// from the session's group claim and read at login by design, so that the
// session lifetime is the revocation window and nothing in the database can
// grant a role (see
// https://mnestor.github.io/ssoossh/internals/invariants/).
type IdentityService struct {
	db *gorm.DB
	// ldap supplies the directory overlay. Nil when LDAP is disabled, which
	// leaves the OIDC values from the users row as the whole answer.
	ldap *LDAPService
}

// NewIdentityService creates an IdentityService. ldap may be nil.
func NewIdentityService(db *gorm.DB, ldap *LDAPService) *IdentityService {
	return &IdentityService{db: db, ldap: ldap}
}

// RefreshIdentity replaces identity's account lists and extra fields with
// what is stored for it, mutating identity in place. Subject is the key, and
// the only field read.
//
// It rebuilds the identity the same way a login does — the users row first,
// then the directory values over the top — so a refreshed session and a
// fresh login agree on what the person holds.
//
// Fails open, and returns an error only for the caller to log: a database
// error leaves the session identity exactly as it was rather than emptying
// the account lists, because a blip that stripped everyone's principals
// mid-request would be a worse outage than briefly stale data. A missing
// users row is not an error at all — it is a session whose row was deleted,
// and there is nothing to refresh from.
func (s *IdentityService) RefreshIdentity(ctx context.Context, identity *Identity) error {
	if s == nil || identity == nil || s.db == nil {
		return nil
	}

	var user model.User
	err := s.db.WithContext(ctx).
		Select("id", "other_accounts", "service_accounts", "extra_fields").
		Where("subject = ?", identity.Subject).
		First(&user).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil
	case err != nil:
		return err
	}

	identity.OtherAccounts = decodeStoredStringList(user.OtherAccounts)
	identity.ServiceAccounts = decodeStoredStringList(user.ServiceAccounts)
	identity.Extra = decodeExtraFields(user.ExtraFields)

	return s.ldap.applyStored(ctx, identity, user.ID)
}

// decodeStoredStringList decodes one of the JSON string-list columns on the
// users row. An empty or malformed column yields nil, which reads as "holds
// none" — the safe direction for a list that gates principals.
func decodeStoredStringList(encoded string) []string {
	if encoded == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		slog.Warn("failed to decode a stored account list; treating it as empty",
			slog.String("error", err.Error()))
		return nil
	}
	return values
}

// EffectiveExtra renders the identity's extra fields as the wire shape: each
// value a string or an array of strings, matching how the claim arrived.
// Always non-nil, so a caller can hand it straight to a JSON response.
//
// This is the merged set — the OIDC claims from the users row with the
// directory values over the top — which is what key ID templates and
// certificate policy evaluate against. A screen that reads the users row on
// its own would show only the OIDC half.
func (i *Identity) EffectiveExtra() map[string]any {
	out := make(map[string]any, len(i.Extra))
	for name, value := range i.Extra {
		if list, ok := value.List(); ok {
			// A copy, so a caller cannot reach back into the identity
			// through the response it was handed.
			out[name] = append([]string{}, list...)
			continue
		}
		out[name] = value.scalar
	}
	return out
}
