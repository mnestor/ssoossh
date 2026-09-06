package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Resolving "who holds this account name" the way authorization does.
//
// The answer lives in two places, not one. users.service_accounts carries
// the OIDC claim as login wrote it, and user_ldap.attributes carries what
// the directory sync last read; IdentityService.RefreshIdentity rebuilds a
// session from the first with the second over the top, and applyLDAPValues
// replaces the list rather than merging it. A query against the users row
// alone therefore answers a different question from the one the server
// authorizes against.
//
// In a directory deployment it answers it wrongly in the direction that
// matters: the sync writes the accounts it resolves to user_ldap and leaves
// the claim column exactly as login found it, which is routinely "null". A
// code every holder could open then had no holders at all — the "Who has
// access" panel said nobody held the account, and expiry reminders and
// redemption notices for it reached nobody unless the code carried an
// explicit notification address.

// accountHolding is one user's relationship to an account name, resolved
// from their effective account lists.
type accountHolding struct {
	User model.User

	// Claimed reports that the service-account list names the account —
	// the ordinary case, and the stronger statement of the two.
	Claimed bool

	// Own reports that the account is the person's own: their username, or
	// an entry in their other-accounts list. Only ever set when
	// cert_options.service.allow_user_accounts is on, since that is the
	// only configuration in which a personal account can carry a code.
	Own bool
}

// usersHoldingAccount returns everyone whose effective account lists name
// accountName, ordered by username.
//
// ldapEnabled must mirror config.LDAPConfig.Enabled. With the directory
// switched off, nothing refreshes user_ldap and nothing removes it, so its
// rows are frozen where they were — reading them then would answer from
// membership no operator can correct from inside the product, which is the
// same reason GroupRecipients ignores directory-sourced group rows.
//
// allowUserAccounts must mirror config.CertOptions.Service.AllowUserAccounts.
//
// The accepted limitation, unchanged: this reaches only users the server
// has a row for, which means people who have logged in at least once or
// whom the directory sync has seen. The server never enumerates a
// directory, so the answer is "everyone known to hold this", not "everyone
// who holds it".
func usersHoldingAccount(ctx context.Context, db *gorm.DB, accountName string, ldapEnabled, allowUserAccounts bool) ([]accountHolding, error) {
	if accountName == "" {
		return nil, nil
	}

	// The quotes are part of the pattern: they make this match a whole
	// element of the stored JSON array rather than any substring of one.
	// Every LIKE here is a prefilter and the decode below is the actual
	// test — the LIKE alone cannot be trusted (a value may sit under some
	// other key of the attributes map, or inside a malformed row) and the
	// decode alone would pull every user row into Go.
	quoted, err := json.Marshal(accountName)
	if err != nil {
		// not covered: json.Marshal cannot fail on a string.
		return nil, fmt.Errorf("failed to encode service account %q: %w", accountName, err)
	}
	pattern := "%" + string(quoted) + "%"

	candidates, err := claimedAccountCandidates(ctx, db, accountName, pattern, allowUserAccounts)
	if err != nil {
		return nil, err
	}

	overlay := map[string]map[string][]string{}
	if ldapEnabled {
		candidates, overlay, err = withDirectoryCandidates(ctx, db, candidates, pattern)
		if err != nil {
			return nil, err
		}
	}

	holdings := make([]accountHolding, 0, len(candidates))
	for _, user := range candidates {
		claimed, own := holdsAccount(ctx, user, overlay[user.ID], accountName, allowUserAccounts)
		if !claimed && !own {
			continue
		}
		holdings = append(holdings, accountHolding{
			User:    user,
			Claimed: claimed,
			// Claimed wins the label where a person has the account both
			// ways: the claim is the stronger statement, and a page's
			// "this is their own account" note would be misleading beside
			// an account a claim also vouches for.
			Own: own && !claimed,
		})
	}

	slices.SortFunc(holdings, func(a, b accountHolding) int {
		return strings.Compare(a.User.Username, b.User.Username)
	})
	return holdings, nil
}

// claimedAccountCandidates reads the users whose own columns mention the
// account: the claim list, and — with allow_user_accounts on — their
// username or other-accounts list.
func claimedAccountCandidates(ctx context.Context, db *gorm.DB, accountName, pattern string, allowUserAccounts bool) ([]model.User, error) {
	q := db.WithContext(ctx).Model(&model.User{}).
		Where("service_accounts LIKE ?", pattern)
	if allowUserAccounts {
		q = q.Or("username = ?", accountName).Or("other_accounts LIKE ?", pattern)
	}

	var candidates []model.User
	if err := q.Find(&candidates).Error; err != nil {
		return nil, fmt.Errorf("failed to resolve the holders of service account %q: %w", accountName, err)
	}
	return candidates, nil
}

// withDirectoryCandidates adds the users the directory names as holders —
// the ones whose claim column never carried the account, because the sync
// is where it came from — and returns the stored attributes of every
// candidate.
//
// Attributes are read for all of them, not only the ones the pattern
// matched: the overlay decides both directions, and a user the claim column
// still names has lost the account if the directory's answer no longer
// carries it.
func withDirectoryCandidates(ctx context.Context, db *gorm.DB, candidates []model.User, pattern string) ([]model.User, map[string]map[string][]string, error) {
	var fromDirectory []model.User
	if err := db.WithContext(ctx).Model(&model.User{}).
		Joins("JOIN user_ldap ON user_ldap.user_id = users.id").
		Where("user_ldap.attributes LIKE ?", pattern).
		Find(&fromDirectory).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to resolve the directory holders of an account: %w", err)
	}

	known := make(map[string]bool, len(candidates))
	for _, user := range candidates {
		known[user.ID] = true
	}
	for _, user := range fromDirectory {
		if !known[user.ID] {
			candidates = append(candidates, user)
			known[user.ID] = true
		}
	}

	overlay := map[string]map[string][]string{}
	if len(candidates) == 0 {
		return candidates, overlay, nil
	}

	ids := make([]string, 0, len(candidates))
	for _, user := range candidates {
		ids = append(ids, user.ID)
	}
	var rows []model.UserLDAP
	if err := db.WithContext(ctx).Where("user_id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to read the directory attributes of an account's holders: %w", err)
	}

	for _, row := range rows {
		var values map[string][]string
		if row.Attributes == "" {
			continue
		}
		if err := json.Unmarshal([]byte(row.Attributes), &values); err != nil {
			// One unreadable row must not decide the answer for everyone
			// else: the user keeps whatever their claim column says, which
			// is what a session with an unreadable overlay also keeps.
			slog.WarnContext(ctx, "skipping unreadable directory attributes while resolving account holders",
				"user_id", row.UserID, "error", err)
			continue
		}
		overlay[row.UserID] = values
	}
	return candidates, overlay, nil
}

// holdsAccount reports how one candidate holds accountName, if at all,
// applying the directory's values over the stored claim the way a session
// refresh does.
func holdsAccount(ctx context.Context, user model.User, directory map[string][]string, accountName string, allowUserAccounts bool) (claimed, own bool) {
	serviceAccounts := decodeAccountList(ctx, user.ID, "service accounts", user.ServiceAccounts)
	otherAccounts := decodeAccountList(ctx, user.ID, "other accounts", user.OtherAccounts)

	// Replaced, not merged — applyLDAPValues assigns, so the directory's
	// answer is the whole answer for a field it carries, including an empty
	// one.
	if values, ok := directory[config.LDAPFieldServiceAccounts]; ok {
		serviceAccounts = values
	}
	if values, ok := directory[config.LDAPFieldOtherAccounts]; ok {
		otherAccounts = values
	}

	claimed = slices.Contains(serviceAccounts, accountName)
	own = allowUserAccounts &&
		(user.Username == accountName || slices.Contains(otherAccounts, accountName))
	return claimed, own
}

// decodeAccountList decodes one of the JSON string-list columns on a users
// row. An empty or unreadable column reads as "holds none", which is the
// safe direction for a list that gates a credential, and the same direction
// decodeStoredStringList takes when a session is rebuilt from these
// columns.
func decodeAccountList(ctx context.Context, userID, what, stored string) []string {
	if stored == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(stored), &values); err != nil {
		slog.WarnContext(ctx, "skipping a user whose stored accounts do not parse",
			"user_id", userID, "field", what, "error", err)
		return nil
	}
	return values
}
