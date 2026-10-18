package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// Fleet key shapes. The id is fixed for the key's life and may be logged;
// the secret is shown once, at creation and at each rotation, and only its
// SHA-256 is stored.
const (
	// nssKeyIDPrefix starts every key id.
	nssKeyIDPrefix = "nss_"
	// nssKeySecretPrefix starts every secret, so one found in a file or a
	// paste is recognisable for what it is.
	nssKeySecretPrefix = "ssoossh_nss_"

	// maxNSSKeyNameLength and maxNSSKeyDescriptionLength bound the free
	// text an administrator types; maxNSSKeyGroupLength bounds a group
	// name, which may be a full DN.
	maxNSSKeyNameLength        = 100
	maxNSSKeyDescriptionLength = 1000
	maxNSSKeyGroupLength       = 512
)

// The status a key is in, derived from its revocation and expiry rather
// than stored.
const (
	// NSSKeyStatusActive is a key hosts may use.
	NSSKeyStatusActive = "active"
	// NSSKeyStatusExpired is a key past its expires_at.
	NSSKeyStatusExpired = "expired"
	// NSSKeyStatusRevoked is a key revoked by someone. Terminal.
	NSSKeyStatusRevoked = "revoked"
)

// What a caller may do with one key, sent with every key so the web UI
// shows exactly the affordances the server will honour.
const (
	// NSSKeyPermissionView reads the key, its usage and its rotation
	// report.
	NSSKeyPermissionView = "view"
	// NSSKeyPermissionRotate issues a new secret, and ends the previous
	// one early.
	NSSKeyPermissionRotate = "rotate"
	// NSSKeyPermissionRevoke revokes the key.
	NSSKeyPermissionRevoke = "revoke"
	// NSSKeyPermissionEdit changes the description, expiry and
	// notification address.
	NSSKeyPermissionEdit = "edit"
	// NSSKeyPermissionAssignGroup changes the group that manages the key.
	NSSKeyPermissionAssignGroup = "assign_group"
)

// NSSKeyService administers the fleet API keys hosts present to the NSS
// lookups, verifies them on every lookup, and reports and mails on their
// use. See docs/proposals/nss-directory/1-server-api.md, "Fleet key
// authentication" and "Fleet key admin endpoints".
//
// Authorization is decided here, from the caller's session groups and the
// key's own group, so the role matrix has one home: admin.nss_key_group
// (and admin) manage every key; a key's group members manage that key; SOC
// may also revoke; auditors may read.
type NSSKeyService struct {
	config *config.Config
	db     *gorm.DB

	// auditor records the nss.key_* events. Nil until SetAuditor runs.
	auditor *AuditService
	// notifier sends the key mail; discards until SetNotifier runs.
	notifier Notifier

	// dummyHash is compared against when the presented key id names no
	// usable key, so an unknown id costs the same work as a wrong secret.
	// The hash of 32 random bytes nobody holds, so nothing matches it.
	dummyHash string

	// now is the clock, time.Now outside tests.
	now func() time.Time
}

// NewNSSKeyService builds the key service. meter registers the
// ssoossh_nss_keys gauge; nil registers nothing.
func NewNSSKeyService(c *config.Config, db *gorm.DB, meter metric.Meter) (*NSSKeyService, error) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return nil, fmt.Errorf("seed the fleet key dummy hash: %w", err)
	}
	s := &NSSKeyService{
		config:    c,
		db:        db,
		notifier:  discardNotifications{},
		dummyHash: hashNSSSecret(hex.EncodeToString(seed)),
		now:       time.Now,
	}
	if meter != nil {
		if err := s.registerKeyGauge(meter); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// SetAuditor wires the audit recorder. Called once at startup.
func (s *NSSKeyService) SetAuditor(a *AuditService) { s.auditor = a }

// SetNotifier wires the mail publisher. Called once at startup.
func (s *NSSKeyService) SetNotifier(n Notifier) { s.notifier = n }

// hashNSSSecret is the stored form of a secret: hex SHA-256. The secret is
// 256 random bits, so no key derivation is needed, and a KDF here would
// make every lookup a CPU cost an attacker could multiply.
func hashNSSSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// newNSSKeyID returns "nss_" and 8 Crockford base32 symbols, the console
// code's shape, lower-cased so it reads as an identifier rather than a
// code to type.
func newNSSKeyID() (string, error) {
	code, err := newUserCode()
	if err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return "", fmt.Errorf("generate a fleet key id: %w", err)
	}
	return nssKeyIDPrefix + strings.ToLower(code), nil
}

// newNSSKeySecret returns a new secret: the prefix and 32 random bytes,
// base64url without padding.
func newNSSKeySecret() (string, error) {
	token, err := randomToken()
	if err != nil {
		// not covered: crypto/rand failure (see .claude/rules/test-go.md).
		return "", fmt.Errorf("generate a fleet key secret: %w", err)
	}
	return nssKeySecretPrefix + token, nil
}

// nssKeyStatus derives a key's status at now.
func nssKeyStatus(key *model.NSSKey, now time.Time) string {
	switch {
	case key.RevokedAt != nil:
		return NSSKeyStatusRevoked
	case !key.ExpiresAt.After(now):
		return NSSKeyStatusExpired
	default:
		return NSSKeyStatusActive
	}
}

// NSSKeyView is one key as a caller sees it: the row, who created and
// revoked it by username, its derived status, and what this caller may do
// with it.
type NSSKeyView struct {
	Key               model.NSSKey
	CreatedByUsername string
	RevokedByUsername string
	Status            string
	Permissions       []string
}

// HasPermission reports whether the view grants permission.
func (v *NSSKeyView) HasPermission(permission string) bool {
	return slices.Contains(v.Permissions, permission)
}

// keyGroupMember reports whether groups holds the key's managing group.
// Exact match, and an empty group never matches, as containsGroup in
// server/config.
func keyGroupMember(groups []string, key *model.NSSKey) bool {
	return key.GroupName != "" && slices.Contains(groups, key.GroupName)
}

// permissions is the role matrix for one key and one caller. A revoked key
// is terminal, so it keeps only view.
func (s *NSSKeyService) permissions(groups []string, key *model.NSSKey) []string {
	keyAdmin := s.config.Admin.GrantsNSSKeyAdmin(groups)
	member := keyGroupMember(groups, key)

	var granted []string
	if keyAdmin || member || s.config.Admin.GrantsAuditor(groups) {
		granted = append(granted, NSSKeyPermissionView)
	}
	if key.RevokedAt != nil {
		return granted
	}
	if keyAdmin || member {
		granted = append(granted, NSSKeyPermissionRotate)
	}
	if keyAdmin || member || s.config.Admin.GrantsSOC(groups) {
		granted = append(granted, NSSKeyPermissionRevoke)
	}
	if keyAdmin || member {
		granted = append(granted, NSSKeyPermissionEdit)
	}
	if keyAdmin {
		granted = append(granted, NSSKeyPermissionAssignGroup)
	}
	return granted
}

// sees reports whether the caller may see every key, rather than only the
// ones their groups manage.
func (s *NSSKeyService) seesAll(groups []string) bool {
	return s.config.Admin.GrantsNSSKeyAdmin(groups) || s.config.Admin.GrantsAuditor(groups)
}

// CanCreate reports whether the identity may create keys: the fleet key
// admin role, which admin implies.
func (s *NSSKeyService) CanCreate(identity *Identity) bool {
	return s.config.Admin.GrantsNSSKeyAdmin(identityGroups(identity))
}

// List returns the keys the identity may view, by name.
func (s *NSSKeyService) List(ctx context.Context, identity *Identity) ([]NSSKeyView, error) {
	groups := identityGroups(identity)
	q := s.db.WithContext(ctx).Order("name, id")
	if !s.seesAll(groups) {
		if len(groups) == 0 {
			return []NSSKeyView{}, nil
		}
		q = q.Where("group_name <> '' AND group_name IN ?", groups)
	}
	var keys []model.NSSKey
	if err := q.Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("list fleet keys: %w", err)
	}
	return s.views(ctx, groups, keys)
}

// Get returns one key, or a 404 when it does not exist or the identity may
// not view it: a key the caller cannot see is not confirmed to exist.
func (s *NSSKeyService) Get(ctx context.Context, identity *Identity, id string) (*NSSKeyView, error) {
	key, err := s.load(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	return s.viewable(ctx, identity, key)
}

// viewable builds the caller's view of key, or the 404 for a key they may
// not view.
func (s *NSSKeyService) viewable(ctx context.Context, identity *Identity, key *model.NSSKey) (*NSSKeyView, error) {
	views, err := s.views(ctx, identityGroups(identity), []model.NSSKey{*key})
	if err != nil {
		return nil, err
	}
	if !views[0].HasPermission(NSSKeyPermissionView) {
		return nil, nssKeyNotFound()
	}
	return &views[0], nil
}

// nssKeyNotFound is the 404 for an unknown or unviewable key.
func nssKeyNotFound() error {
	return &errorresponses.NotFoundError{Resource: "fleet key"}
}

// load reads one key by id through db, or the 404.
func (s *NSSKeyService) load(ctx context.Context, db *gorm.DB, id string) (*model.NSSKey, error) {
	var key model.NSSKey
	err := db.WithContext(ctx).Where("id = ?", id).Take(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nssKeyNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("read fleet key: %w", err)
	}
	return &key, nil
}

// views turns rows into views for a caller holding groups, resolving the
// creator and revoker usernames in one query.
func (s *NSSKeyService) views(ctx context.Context, groups []string, keys []model.NSSKey) ([]NSSKeyView, error) {
	ids := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		ids = append(ids, key.CreatedBy)
		if key.RevokedBy != nil {
			ids = append(ids, *key.RevokedBy)
		}
	}
	usernames := map[string]string{}
	if len(ids) > 0 {
		var users []model.User
		if err := s.db.WithContext(ctx).Select("id, username").Where("id IN ?", ids).Find(&users).Error; err != nil {
			return nil, fmt.Errorf("resolve fleet key creators: %w", err)
		}
		for _, user := range users {
			usernames[user.ID] = user.Username
		}
	}

	now := s.now()
	out := make([]NSSKeyView, 0, len(keys))
	for _, key := range keys {
		view := NSSKeyView{
			Key:               key,
			CreatedByUsername: usernames[key.CreatedBy],
			Status:            nssKeyStatus(&key, now),
			Permissions:       s.permissions(groups, &key),
		}
		if key.RevokedBy != nil {
			view.RevokedByUsername = usernames[*key.RevokedBy]
		}
		out = append(out, view)
	}
	return out, nil
}

// identityGroups is the identity's groups, nil-safe.
func identityGroups(identity *Identity) []string {
	if identity == nil {
		return nil
	}
	return identity.Groups
}

// NSSKeyCreate is what a caller supplies for a new key. Nil fields take
// their defaults: expires_at now plus nss.key_default_lifetime, the
// notification address the creator's own.
type NSSKeyCreate struct {
	Name              string
	Description       string
	GroupName         string
	ExpiresAt         *time.Time
	NotificationEmail *string
}

// NSSKeyUpdate is a change to a key; nil fields are left alone.
// GroupName needs assign_group, the rest edit.
type NSSKeyUpdate struct {
	Description       *string
	ExpiresAt         *time.Time
	NotificationEmail *string
	GroupName         *string
}

// nssActor is the calling person, resolved to their users row.
type nssActor struct {
	identity *Identity
	user     model.User
}

// resolveActor loads the identity's users row. A session that has outlived
// its row cannot be named as a creator or revoker, so it is refused.
func (s *NSSKeyService) resolveActor(ctx context.Context, identity *Identity) (*nssActor, error) {
	if identity == nil {
		// not covered: every caller comes from middleware.Identity, which
		// reports absence separately.
		return nil, &errorresponses.UnauthorizedError{}
	}
	var user model.User
	err := s.db.WithContext(ctx).Where("subject = ?", identity.Subject).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &errorresponses.ForbiddenError{Reason: "no user record for the authenticated identity"}
	}
	if err != nil {
		return nil, fmt.Errorf("resolve the user record: %w", err)
	}
	return &nssActor{identity: identity, user: user}, nil
}

// subject snapshots the actor for an audit event.
func (a *nssActor) subject() *AuditSubject {
	return AuditSubjectFromIdentity(a.identity, a.user.ID)
}

// displayName is the actor's name for mail, else empty.
func (a *nssActor) displayName() string {
	if a.identity.DisplayName != "" {
		return a.identity.DisplayName
	}
	return a.user.DisplayName
}

// Create makes a key and returns it with its secret, which is never
// available again. Only admin.nss_key_group and admins may create keys.
func (s *NSSKeyService) Create(ctx context.Context, identity *Identity, in NSSKeyCreate, sourceIP string) (*NSSKeyView, string, error) {
	if !s.CanCreate(identity) {
		return nil, "", &errorresponses.ForbiddenError{Reason: "creating a fleet key needs the fleet key admin role"}
	}
	actor, err := s.resolveActor(ctx, identity)
	if err != nil {
		return nil, "", err
	}
	now := s.now()

	name, err := boundedText("name", in.Name, maxNSSKeyNameLength, true)
	if err != nil {
		return nil, "", err
	}
	description, err := boundedText("description", in.Description, maxNSSKeyDescriptionLength, false)
	if err != nil {
		return nil, "", err
	}
	group, err := boundedText("group_name", in.GroupName, maxNSSKeyGroupLength, false)
	if err != nil {
		return nil, "", err
	}
	expiresAt := now.Add(s.config.NSS.KeyDefaultLifetime)
	if in.ExpiresAt != nil {
		expiresAt = *in.ExpiresAt
	}
	if !expiresAt.After(now) {
		return nil, "", &errorresponses.InvalidRequestError{Reason: "expires_at must be in the future"}
	}
	email := actor.user.Email
	if email == "" {
		email = identity.Email
	}
	if in.NotificationEmail != nil {
		email = *in.NotificationEmail
	}
	if email, err = validateNotificationEmail(email); err != nil {
		return nil, "", err
	}

	secret, err := newNSSKeySecret()
	if err != nil {
		return nil, "", err
	}
	key := model.NSSKey{
		CurrentGeneration: 1,
		SecretHash:        hashNSSSecret(secret),
		Name:              name,
		Description:       description,
		CreatedBy:         actor.user.ID,
		GroupName:         group,
		ExpiresAt:         expiresAt.UTC(),
		NotificationEmail: email,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	var event AuditEvent
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.insertWithFreshID(tx, &key); err != nil {
			return err
		}
		event = AuditEvent{
			Action: AuditNSSKeyCreated,
			Actor:  actor.subject(),
			Detail: map[string]any{
				"key_id":             key.ID,
				"name":               key.Name,
				"group_name":         key.GroupName,
				"expires_at":         key.ExpiresAt,
				"notification_email": key.NotificationEmail,
				"source_ip":          sourceIP,
			},
		}
		return s.recordTx(tx, event)
	})
	if err != nil {
		return nil, "", err
	}
	s.logOnly(event)

	view, err := s.viewable(ctx, identity, &key)
	if err != nil {
		// not covered: the creator holds the key admin role, which views
		// every key.
		return nil, "", err
	}
	return view, secret, nil
}

// insertWithFreshID gives key a new id and inserts it, drawing again on
// the vanishingly rare collision with an existing id.
func (s *NSSKeyService) insertWithFreshID(tx *gorm.DB, key *model.NSSKey) error {
	for attempt := 0; ; attempt++ {
		id, err := newNSSKeyID()
		if err != nil {
			return err
		}
		key.ID = id
		err = tx.Create(key).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrDuplicatedKey) || attempt >= 2 {
			return fmt.Errorf("create fleet key: %w", err)
		}
		// not covered: a 40-bit id colliding three times running.
	}
}

// boundedText trims value and checks it against limit, and against empty
// when required. field names the input in the 400.
func boundedText(field, value string, limit int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", &errorresponses.InvalidRequestError{Reason: field + " is required"}
	}
	if len(value) > limit {
		return "", &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("%s is %d characters, over the %d limit", field, len(value), limit)}
	}
	return value, nil
}

// mutate loads a key for a change, checks the caller holds every one of
// permissions on it and that it is not revoked, then runs change in a
// transaction with the key locked to its current generation, recording the
// event it returns. The 404 for a key the caller cannot view comes before
// any 403, so a key is never confirmed to someone who may not see it.
func (s *NSSKeyService) mutate(ctx context.Context, identity *Identity, id string, permissions []string,
	change func(tx *gorm.DB, actor *nssActor, key *model.NSSKey, now time.Time) (*AuditEvent, error)) (*NSSKeyView, *model.NSSKey, error) {
	key, err := s.load(ctx, s.db, id)
	if err != nil {
		return nil, nil, err
	}
	view, err := s.viewable(ctx, identity, key)
	if err != nil {
		return nil, nil, err
	}
	if key.RevokedAt != nil {
		return nil, nil, &errorresponses.ConflictError{Reason: "the fleet key is revoked"}
	}
	for _, permission := range permissions {
		if !view.HasPermission(permission) {
			return nil, nil, &errorresponses.ForbiddenError{Reason: "you may not " + strings.ReplaceAll(permission, "_", " ") + " this fleet key"}
		}
	}
	actor, err := s.resolveActor(ctx, identity)
	if err != nil {
		return nil, nil, err
	}

	var event *AuditEvent
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.RevokedAt != nil || current.CurrentGeneration != key.CurrentGeneration {
			return &errorresponses.ConflictError{Reason: "the fleet key changed while this request was made; reload it and try again"}
		}
		event, err = change(tx, actor, current, s.now())
		if err != nil || event == nil {
			return err
		}
		return s.recordTx(tx, *event)
	})
	if err != nil {
		return nil, nil, err
	}
	if event != nil {
		s.logOnly(*event)
	}
	updated, err := s.load(ctx, s.db, id)
	if err != nil {
		return nil, nil, err
	}
	view, err = s.viewable(ctx, identity, updated)
	if err != nil {
		// not covered: only the key admin role may move a key to another
		// group, and it views every key, so a caller never loses sight of
		// a key by changing it.
		return nil, updated, err
	}
	return view, updated, nil
}

// Update changes a key's description, expiry, notification address or
// group. Changing the expiry re-arms the expiring reminder.
func (s *NSSKeyService) Update(ctx context.Context, identity *Identity, id string, in NSSKeyUpdate, sourceIP string) (*NSSKeyView, error) {
	if in.Description == nil && in.ExpiresAt == nil && in.NotificationEmail == nil && in.GroupName == nil {
		return nil, &errorresponses.InvalidRequestError{Reason: "nothing to change: send description, expires_at, notification_email or group_name"}
	}
	needed := []string{}
	if in.Description != nil || in.ExpiresAt != nil || in.NotificationEmail != nil {
		needed = append(needed, NSSKeyPermissionEdit)
	}
	if in.GroupName != nil {
		needed = append(needed, NSSKeyPermissionAssignGroup)
	}

	view, _, err := s.mutate(ctx, identity, id, needed, func(tx *gorm.DB, actor *nssActor, key *model.NSSKey, now time.Time) (*AuditEvent, error) {
		updates, changes, err := s.updates(key, in, now)
		if err != nil || len(changes) == 0 {
			return nil, err
		}
		updates["updated_at"] = now
		if err := tx.Model(&model.NSSKey{}).Where("id = ?", key.ID).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("update fleet key: %w", err)
		}
		return &AuditEvent{
			Action: AuditNSSKeyUpdated,
			Actor:  actor.subject(),
			Detail: map[string]any{"key_id": key.ID, "changes": changes, "source_ip": sourceIP},
		}, nil
	})
	return view, err
}

// updates validates in against key and returns the column updates and,
// per changed field, its value before and after. A field set to the value
// it already has is not a change.
func (s *NSSKeyService) updates(key *model.NSSKey, in NSSKeyUpdate, now time.Time) (map[string]any, map[string]any, error) {
	updates := map[string]any{}
	changes := map[string]any{}
	change := func(field string, before, after any) {
		updates[field] = after
		changes[field] = map[string]any{"before": before, "after": after}
	}
	if in.Description != nil {
		description, err := boundedText("description", *in.Description, maxNSSKeyDescriptionLength, false)
		if err != nil {
			return nil, nil, err
		}
		if description != key.Description {
			change("description", key.Description, description)
		}
	}
	if in.ExpiresAt != nil {
		if !in.ExpiresAt.After(now) {
			return nil, nil, &errorresponses.InvalidRequestError{Reason: "expires_at must be in the future"}
		}
		if !in.ExpiresAt.Equal(key.ExpiresAt) {
			change("expires_at", key.ExpiresAt, in.ExpiresAt.UTC())
			// The reminder cadence restarts from the new date, as an
			// enrollment's does when its expiry moves.
			updates["expiry_reminder_sent_at"] = nil
		}
	}
	if in.NotificationEmail != nil {
		email, err := validateNotificationEmail(*in.NotificationEmail)
		if err != nil {
			return nil, nil, err
		}
		if email != key.NotificationEmail {
			change("notification_email", key.NotificationEmail, email)
		}
	}
	if in.GroupName != nil {
		group, err := boundedText("group_name", *in.GroupName, maxNSSKeyGroupLength, false)
		if err != nil {
			return nil, nil, err
		}
		if group != key.GroupName {
			change("group_name", key.GroupName, group)
		}
	}
	return updates, changes, nil
}

// Rotate issues a new secret and returns it with the key; it is never
// available again. The current secret becomes the previous one, accepted
// until now plus nss.key_rotation_grace, so hosts can switch over.
func (s *NSSKeyService) Rotate(ctx context.Context, identity *Identity, id, sourceIP string) (*NSSKeyView, string, error) {
	secret, err := newNSSKeySecret()
	if err != nil {
		return nil, "", err
	}
	var rotatedBy *nssActor
	view, key, err := s.mutate(ctx, identity, id, []string{NSSKeyPermissionRotate}, func(tx *gorm.DB, actor *nssActor, key *model.NSSKey, now time.Time) (*AuditEvent, error) {
		rotatedBy = actor
		previousExpires := now.Add(s.config.NSS.KeyRotationGrace).UTC()
		generation := key.CurrentGeneration + 1
		result := tx.Model(&model.NSSKey{}).
			Where("id = ? AND current_generation = ?", key.ID, key.CurrentGeneration).
			Updates(map[string]any{
				"secret_hash":              hashNSSSecret(secret),
				"previous_secret_hash":     key.SecretHash,
				"previous_expires_at":      previousExpires,
				"current_generation":       generation,
				"rotated_at":               now.UTC(),
				"rotation_cleanup_sent_at": nil,
				"updated_at":               now,
			})
		if result.Error != nil {
			return nil, fmt.Errorf("rotate fleet key: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			// not covered: the row was read under the same generation in
			// this transaction.
			return nil, &errorresponses.ConflictError{Reason: "the fleet key was rotated by someone else; reload it"}
		}
		return &AuditEvent{
			Action: AuditNSSKeyRotated,
			Actor:  actor.subject(),
			Detail: map[string]any{
				"key_id":              key.ID,
				"generation":          generation,
				"previous_expires_at": previousExpires,
				"source_ip":           sourceIP,
			},
		}, nil
	})
	if key == nil {
		return nil, "", err
	}
	if rotatedBy != nil {
		s.notifyRotated(ctx, key, rotatedBy)
	}
	return view, secret, err
}

// ClearPrevious ends the previous secret before its grace runs out, once
// every host has switched.
func (s *NSSKeyService) ClearPrevious(ctx context.Context, identity *Identity, id, sourceIP string) (*NSSKeyView, error) {
	view, _, err := s.mutate(ctx, identity, id, []string{NSSKeyPermissionRotate}, func(tx *gorm.DB, actor *nssActor, key *model.NSSKey, now time.Time) (*AuditEvent, error) {
		if key.PreviousSecretHash == nil {
			return nil, &errorresponses.ConflictError{Reason: "the fleet key has no previous secret"}
		}
		if err := tx.Model(&model.NSSKey{}).Where("id = ?", key.ID).Updates(map[string]any{
			"previous_secret_hash": nil,
			"previous_expires_at":  nil,
			"updated_at":           now,
		}).Error; err != nil {
			return nil, fmt.Errorf("clear the previous fleet key secret: %w", err)
		}
		return &AuditEvent{
			Action: AuditNSSKeyPreviousCleared,
			Actor:  actor.subject(),
			Detail: map[string]any{"key_id": key.ID, "generation": key.CurrentGeneration - 1, "source_ip": sourceIP},
		}, nil
	})
	return view, err
}

// Revoke ends the key for good: neither secret is accepted again.
func (s *NSSKeyService) Revoke(ctx context.Context, identity *Identity, id, sourceIP string) (*NSSKeyView, error) {
	var revokedBy *nssActor
	view, key, err := s.mutate(ctx, identity, id, []string{NSSKeyPermissionRevoke}, func(tx *gorm.DB, actor *nssActor, key *model.NSSKey, now time.Time) (*AuditEvent, error) {
		revokedBy = actor
		if err := tx.Model(&model.NSSKey{}).Where("id = ?", key.ID).Updates(map[string]any{
			"revoked_at": now.UTC(),
			"revoked_by": actor.user.ID,
			"updated_at": now,
		}).Error; err != nil {
			return nil, fmt.Errorf("revoke fleet key: %w", err)
		}
		return &AuditEvent{
			Action: AuditNSSKeyRevoked,
			Actor:  actor.subject(),
			Detail: map[string]any{"key_id": key.ID, "name": key.Name, "source_ip": sourceIP},
		}, nil
	})
	if key != nil && revokedBy != nil {
		s.notifyRevoked(ctx, key, revokedBy)
	}
	return view, err
}

// recordTx records event in tx when an auditor is wired.
func (s *NSSKeyService) recordTx(tx *gorm.DB, event AuditEvent) error {
	if s.auditor == nil {
		return nil
	}
	return s.auditor.RecordTx(tx, event)
}

// logOnly emits the shipped log line for an event already recorded.
func (s *NSSKeyService) logOnly(event AuditEvent) {
	if s.auditor != nil {
		s.auditor.LogOnly(event)
	}
}

// registerKeyGauge registers ssoossh_nss_keys, the number of keys in each
// status, read from the database at each collection.
func (s *NSSKeyService) registerKeyGauge(meter metric.Meter) error {
	gauge, err := meter.Int64ObservableGauge("ssoossh_nss_keys",
		metric.WithDescription("NSS fleet API keys by status"),
		metric.WithUnit("{key}"))
	if err != nil {
		return fmt.Errorf("create the ssoossh_nss_keys gauge: %w", err)
	}
	_, err = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		counts, err := s.countByStatus(ctx)
		if err != nil {
			return err
		}
		for _, status := range []string{NSSKeyStatusActive, NSSKeyStatusExpired, NSSKeyStatusRevoked} {
			o.ObserveInt64(gauge, counts[status], metric.WithAttributes(nssAttr("status", status)))
		}
		return nil
	}, gauge)
	if err != nil {
		return fmt.Errorf("register the ssoossh_nss_keys callback: %w", err)
	}
	return nil
}

// countByStatus counts the keys in each status at now.
func (s *NSSKeyService) countByStatus(ctx context.Context) (map[string]int64, error) {
	var rows []struct {
		Status string `gorm:"column:status"`
		Count  int64  `gorm:"column:n"`
	}
	err := s.db.WithContext(ctx).Model(&model.NSSKey{}).
		Select(`CASE WHEN revoked_at IS NOT NULL THEN ? WHEN expires_at <= ? THEN ? ELSE ? END AS status, COUNT(*) AS n`,
			NSSKeyStatusRevoked, s.now().UTC(), NSSKeyStatusExpired, NSSKeyStatusActive).
		Group("status").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("count fleet keys by status: %w", err)
	}
	counts := map[string]int64{}
	for _, row := range rows {
		counts[row.Status] = row.Count
	}
	return counts, nil
}
