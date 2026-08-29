package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/mail"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// Notifier is the publishing half of notifications, as the certificate
// paths see it. An interface so those paths can be tested without a
// broker, and so a deployment with mail disabled can be handed a service
// that does nothing rather than a nil check at every call site.
type Notifier interface {
	// Notify addresses one user by users.id.
	Notify(ctx context.Context, kind notify.Kind, userID string, payload any)

	// NotifyServiceAccount addresses every holder of a service account,
	// resolved at delivery. Enrollment-scoped notifications use this: a
	// service enrollment is owned by everyone holding its account, so
	// there is no single user to name (see
	// docs/proposals/enrollment-group-ownership.md).
	NotifyServiceAccount(ctx context.Context, kind notify.Kind, serviceAccount string, payload any)
}

// KindPreference is one notification kind as the preferences page sees it:
// the registry's description of it, plus this user's answer.
type KindPreference struct {
	Kind        notify.Kind
	Title       string
	Description string
	Enabled     bool
}

// NotificationService publishes notification events and owns the stored
// per-user preferences.
//
// Publication is all it does on the request path. Rendering, the preference
// lookup, and SMTP all happen in NotificationHandler, on the broker's own
// goroutines — see Notify.
type NotificationService struct {
	db        *gorm.DB
	publisher message.Publisher

	// enabled mirrors config.MailConfig.Enabled. With it false, Notify is
	// a no-op: no delivery handler is registered either, so a published
	// event would sit unconsumed.
	enabled bool
}

// NewNotificationService constructs the service. enabled comes from
// config.MailConfig.Enabled.
func NewNotificationService(db *gorm.DB, publisher message.Publisher, enabled bool) *NotificationService {
	return &NotificationService{db: db, publisher: publisher, enabled: enabled}
}

// Notify queues one notification and returns. It never blocks on the mail
// relay, and it never returns an error.
//
// Both properties are deliberate. This is called from inside an approval
// and from inside a code redemption — one has a browser waiting on it, the
// other an unattended job — and neither outcome should depend on a mail
// server. A notification that cannot be queued is logged and dropped: the
// certificate work it describes already succeeded, and failing it after the
// fact to report a mail problem would trade a real outcome for a cosmetic
// one.
//
// ctx is used for nothing but the publish itself, and deliberately does not
// travel with the event: the request context is canceled the moment the
// caller has its answer, which is typically before delivery has started.
func (s *NotificationService) Notify(ctx context.Context, kind notify.Kind, userID string, payload any) {
	if userID == "" {
		// Nothing to deliver to. Worth a log line rather than silence: it
		// means a calling path lost track of who it was acting for.
		slog.WarnContext(ctx, "skipping a notification with no recipient", "kind", kind)
		return
	}
	s.publish(ctx, kind, func() (notify.Event, error) {
		return notify.NewEvent(kind, userID, payload)
	})
}

// NotifyServiceAccount queues one notification for every holder of
// serviceAccount. Same non-blocking, never-failing contract as Notify —
// see there for why both properties are deliberate.
//
// The holders are deliberately not resolved here. This is called from
// inside an approval and from inside a redemption, and who owns the
// account is a question for delivery time, not publication time.
func (s *NotificationService) NotifyServiceAccount(ctx context.Context, kind notify.Kind, serviceAccount string, payload any) {
	if serviceAccount == "" {
		// An enrollment whose stored principals never parsed. It is owned
		// by nobody, so there is nobody to tell — but the caller thought
		// there was, which is worth saying out loud.
		slog.WarnContext(ctx, "skipping a notification for an enrollment with no service account", "kind", kind)
		return
	}
	s.publish(ctx, kind, func() (notify.Event, error) {
		return notify.NewServiceAccountEvent(kind, serviceAccount, payload)
	})
}

// publish builds and queues an event, whichever way it is addressed.
func (s *NotificationService) publish(ctx context.Context, kind notify.Kind, build func() (notify.Event, error)) {
	if !s.enabled {
		return
	}

	event, err := build()
	if err != nil {
		slog.ErrorContext(ctx, "failed to build a notification event", "kind", kind, "error", err)
		return
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		// not covered: Event is a plain struct whose only dynamic member
		// is an already-marshalled json.RawMessage.
		slog.ErrorContext(ctx, "failed to encode a notification event", "kind", kind, "error", err)
		return
	}

	if err := s.publisher.Publish(notify.Topic, message.NewMessage(watermill.NewUUID(), encoded)); err != nil {
		slog.ErrorContext(ctx, "failed to queue a notification", "kind", kind, "error", err)
	}
}

// Preferences returns every registered kind with this user's answer,
// in registry order so the page's list is stable between loads.
//
// Kinds the user has never answered take their registered default, and
// stored rows for kinds this build does not register are ignored — see
// model.NotificationPreference for why both are on purpose.
func (s *NotificationService) Preferences(ctx context.Context, userID string) ([]KindPreference, error) {
	stored, err := s.storedPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}

	definitions := notify.Definitions()
	prefs := make([]KindPreference, 0, len(definitions))
	for _, def := range definitions {
		enabled := def.DefaultEnabled
		if choice, ok := stored[def.Kind]; ok {
			enabled = choice
		}
		prefs = append(prefs, KindPreference{
			Kind:        def.Kind,
			Title:       def.Title,
			Description: def.Description,
			Enabled:     enabled,
		})
	}
	return prefs, nil
}

// SetPreferences records the user's answers for the kinds named in
// updates, leaving every other kind alone.
//
// Every kind is validated before anything is written, and the writes share
// one transaction: a half-applied update would leave the page showing
// choices the user did not make, and a rejected one must change nothing.
func (s *NotificationService) SetPreferences(ctx context.Context, userID string, updates map[notify.Kind]bool) error {
	for kind := range updates {
		if _, ok := notify.Lookup(kind); !ok {
			// The caller sent something this build does not know about —
			// a typo, or a page from a newer version. Their input, not a
			// server fault, so it renders as a 400 rather than a 500.
			return &errorresponses.InvalidRequestError{
				Reason: fmt.Sprintf("unknown notification kind %q", kind),
			}
		}
	}
	if len(updates) == 0 {
		return nil
	}

	now := time.Now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for kind, enabled := range updates {
			pref := model.NotificationPreference{
				ID:        uuid.NewString(),
				UserID:    userID,
				Kind:      string(kind),
				Enabled:   enabled,
				CreatedAt: now,
				UpdatedAt: now,
			}
			// Upsert on the (user_id, kind) unique index: the page sends
			// the whole set every time, so a second save must update the
			// row rather than collide with it.
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}, {Name: "kind"}},
				DoUpdates: clause.Assignments(map[string]any{"enabled": enabled, "updated_at": now}),
			}).Create(&pref).Error; err != nil {
				return fmt.Errorf("failed to save the %s notification preference: %w", kind, err)
			}
		}
		return nil
	})
}

// NotificationSettings is the preferences page's whole payload: whether
// the server can send mail at all, where it would send, and the per-kind
// answers.
type NotificationSettings struct {
	// MailEnabled mirrors config.MailConfig.Enabled. The page needs it to
	// explain why toggles it is happily storing produce no mail, rather
	// than leaving the user to conclude the feature is broken.
	MailEnabled bool

	// Address is where notifications would go, read from the users table.
	// Empty when the identity provider releases no email claim, which is
	// the other reason nothing arrives.
	Address string

	Kinds []KindPreference
}

// MailEnabled reports whether the server is configured to send mail.
func (s *NotificationService) MailEnabled() bool { return s.enabled }

// PreferencesForIdentity is Preferences for a session identity, resolving
// the users row by OIDC subject the same way every other user-scoped read
// does.
func (s *NotificationService) PreferencesForIdentity(ctx context.Context, identity *Identity) (NotificationSettings, error) {
	user, err := s.resolveUser(ctx, identity)
	if err != nil {
		return NotificationSettings{}, err
	}

	kinds, err := s.Preferences(ctx, user.ID)
	if err != nil {
		return NotificationSettings{}, err
	}

	return NotificationSettings{
		MailEnabled: s.enabled,
		Address:     user.Email,
		Kinds:       kinds,
	}, nil
}

// SetPreferencesForIdentity is SetPreferences for a session identity.
func (s *NotificationService) SetPreferencesForIdentity(ctx context.Context, identity *Identity, updates map[notify.Kind]bool) error {
	user, err := s.resolveUser(ctx, identity)
	if err != nil {
		return err
	}
	return s.SetPreferences(ctx, user.ID, updates)
}

// resolveUser maps a session identity to its users row, keyed on the OIDC
// subject. The row is written at login (AuthService.upsertUser), so a miss
// means the session outlived its user record — which must fail closed
// rather than fall back to a default set of preferences that would then be
// saved against nobody.
func (s *NotificationService) resolveUser(ctx context.Context, identity *Identity) (model.User, error) {
	if identity == nil {
		// not covered: every caller comes from middleware.Identity, which
		// reports absence separately.
		return model.User{}, &errorresponses.UnauthorizedError{}
	}

	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.User{}, &errorresponses.ForbiddenError{Reason: "no user record for the authenticated identity"}
		}
		return model.User{}, fmt.Errorf("failed to look up the notification recipient: %w", err)
	}
	return user, nil
}

// storedPreferences reads this user's explicit choices, keyed by kind.
func (s *NotificationService) storedPreferences(ctx context.Context, userID string) (map[notify.Kind]bool, error) {
	var rows []model.NotificationPreference
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to read notification preferences: %w", err)
	}

	stored := make(map[notify.Kind]bool, len(rows))
	for _, row := range rows {
		stored[notify.Kind(row.Kind)] = row.Enabled
	}
	return stored, nil
}

// enabledFor reports whether userID wants kind, falling back to the
// registered default when they have never said.
func (s *NotificationService) enabledFor(ctx context.Context, userID string, kind notify.Kind) (bool, error) {
	var pref model.NotificationPreference
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND kind = ?", userID, string(kind)).
		First(&pref).Error
	if err == nil {
		return pref.Enabled, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return notify.DefaultEnabled(kind), nil
	}
	return false, fmt.Errorf("failed to read the %s notification preference: %w", kind, err)
}

// NotificationHandler consumes notify.Topic and delivers what it finds:
// resolve the recipient, check the preference, render, send.
//
// It lives here rather than in server/mail for the same reason
// SignedReplyHandler lives in this package — it needs the database, and
// server/mail is deliberately kept free of it so the rendering and
// transport code can be tested without one.
type NotificationHandler struct {
	db       *gorm.DB
	renderer *mail.Renderer
	sender   mail.Sender
}

// NewNotificationHandler constructs the delivery handler.
func NewNotificationHandler(db *gorm.DB, renderer *mail.Renderer, sender mail.Sender) *NotificationHandler {
	return &NotificationHandler{db: db, renderer: renderer, sender: sender}
}

// Register adds the notification consumer to r.
func (h *NotificationHandler) Register(r *message.Router, subscriber message.Subscriber) {
	r.AddConsumerHandler("notification-send", notify.Topic, subscriber, h.handle)
}

// handle delivers one notification, to the one user it names or to every
// holder of the service account it names.
//
// The distinction that matters here is permanent versus temporary. A
// message that cannot be parsed, a kind this build does not know, a
// recipient set that is empty or that wants none of this — none of those
// get better on redelivery, so they are acknowledged with a log line. A
// relay that would not take the message might, so that nacks and the
// router's retry middleware backs off and tries again.
//
// A fan-out that fails partway is retried whole, so a holder already
// reached can receive a second copy. That is the at-least-once contract
// the single-recipient path has always had, and the failure it exists for
// — a relay that is down — is total rather than per-recipient.
func (h *NotificationHandler) handle(msg *message.Message) error {
	ctx := msg.Context()

	var event notify.Event
	if err := json.Unmarshal(msg.Payload, &event); err != nil {
		slog.ErrorContext(ctx, "discarding an unparseable notification event", "error", err)
		return nil
	}

	payload, err := event.DecodePayload()
	if err != nil {
		slog.ErrorContext(ctx, "discarding a notification event this build cannot render",
			"kind", event.Kind, "error", err)
		return nil
	}

	recipients, err := h.recipients(ctx, event)
	if err != nil {
		return err
	}

	// Preferences are read at delivery, not at publication: an event that
	// waited through a retry backoff has to respect the answer each
	// recipient gave in the meantime.
	wanted := make([]model.User, 0, len(recipients))
	for _, user := range recipients {
		enabled, err := (&NotificationService{db: h.db}).enabledFor(ctx, user.ID, event.Kind)
		if err != nil {
			return err
		}
		if enabled {
			wanted = append(wanted, user)
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	// Rendered once for everyone: the payload describes the event, not the
	// reader, so N recipients get N copies of one message.
	rendered, err := h.renderer.Render(event.Kind, payload)
	if err != nil {
		// A template that does not render will not render on redelivery
		// either: every template is parsed and executed at startup, so
		// reaching here means the payload itself is the problem.
		slog.ErrorContext(ctx, "discarding a notification that could not be rendered",
			"kind", event.Kind, "error", err)
		return nil
	}

	for _, user := range wanted {
		if err := h.sender.Send(ctx, mail.Outgoing{To: user.Email, Rendered: rendered}); err != nil {
			return fmt.Errorf("failed to send the %s notification: %w", event.Kind, err)
		}
		slog.InfoContext(ctx, "notification sent", "kind", event.Kind, "user_id", user.ID)
	}
	return nil
}

// recipients resolves who an event should reach: the one user it names, or
// every holder of the service account it names.
//
// An empty result is a normal outcome, not an error — a user who has since
// been deleted, an identity provider that releases no email claim, a
// service account nobody who has logged in holds — so it is logged and the
// message acknowledged rather than retried.
func (h *NotificationHandler) recipients(ctx context.Context, event notify.Event) ([]model.User, error) {
	if event.ServiceAccount != "" {
		holders, err := (&NotificationService{db: h.db}).ServiceAccountRecipients(ctx, event.ServiceAccount)
		if err != nil {
			return nil, err
		}
		if len(holders) == 0 {
			slog.InfoContext(ctx, "no reachable holders for a service account notification",
				"kind", event.Kind, "service_account", event.ServiceAccount)
		}
		return holders, nil
	}

	var user model.User
	if err := h.db.WithContext(ctx).First(&user, "id = ?", event.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			slog.WarnContext(ctx, "dropping a notification for a user that no longer exists",
				"kind", event.Kind, "user_id", event.UserID)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to look up the notification recipient: %w", err)
	}
	if user.Email == "" {
		// Common rather than exceptional: an identity provider that does
		// not release an email claim leaves every user here.
		slog.DebugContext(ctx, "skipping a notification for a user with no address",
			"kind", event.Kind, "user_id", event.UserID)
		return nil, nil
	}
	return []model.User{user}, nil
}

// discardNotifications is the Notifier a service holds until bootstrap
// attaches a real one. Every certificate path can then call Notify
// unconditionally: a deployment with mail disabled, and every test that
// never wires notifications up, get a no-op instead of a nil check.
type discardNotifications struct{}

// Notify discards the notification.
func (discardNotifications) Notify(context.Context, notify.Kind, string, any) {}

// NotifyServiceAccount discards the notification.
func (discardNotifications) NotifyServiceAccount(context.Context, notify.Kind, string, any) {}

// NotificationPreferenceProvider is the preferences controller's view of
// NotificationService: read and write the caller's own answers, nothing
// else. An interface so the HTTP contract can be tested without a database
// behind it.
type NotificationPreferenceProvider interface {
	PreferencesForIdentity(ctx context.Context, identity *Identity) (NotificationSettings, error)
	SetPreferencesForIdentity(ctx context.Context, identity *Identity, updates map[notify.Kind]bool) error
}

// GroupRecipients resolves the users a group-targeted notification should
// reach: everyone recorded in groupName, from either capture source, who is
// enabled and has an email address.
//
// This is the whole of group fan-out — a recipient resolver, not a new
// subsystem — and it is what user_groups exists for. The rows are a
// snapshot for reaching people, never an authorization input (see
// docs/internals/invariants.md).
//
// Accepted limitation: fan-out reaches only users who have logged in at
// least once, because only they have a row. The server does not create
// shadow users for directory members who have never authenticated;
// enumerating a directory to email strangers is a different feature with
// different consent implications.
func (s *NotificationService) GroupRecipients(ctx context.Context, groupName string) ([]model.User, error) {
	if groupName == "" {
		return nil, nil
	}

	var users []model.User
	err := s.db.WithContext(ctx).
		Joins("JOIN user_groups ON user_groups.user_id = users.id").
		Where("user_groups.group_name = ?", groupName).
		// Disabled accounts are excluded: a disable removes fan-out
		// eligibility, which is also why stale LDAP rows left behind by a
		// vanished entry cost nothing once the disable lands.
		Where("users.disabled_at IS NULL").
		Where("users.email <> ''").
		Distinct().
		Find(&users).Error
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the recipients of group %q: %w", groupName, err)
	}
	return users, nil
}

// ServiceAccountRecipients resolves who an enrollment-scoped notification
// should reach: everyone holding accountName who is enabled and has an
// email address.
//
// This is the delivery half of group ownership (see
// docs/proposals/enrollment-group-ownership.md). With no single owning
// user there is no single address to send to, so the recipient set is
// exactly the set of owners, resolved fresh at delivery rather than
// captured when the event was published.
//
// # Matching a JSON column
//
// users.service_accounts is a JSON-encoded []string, and no portable SQL
// expression indexes into it, so the match is done in two steps: the query
// narrows on the quoted name as a substring, and the result is confirmed
// by actually decoding each candidate. The LIKE alone would be wrong (it
// cannot distinguish a name that is a prefix of another once quotes are
// stripped by a malformed row) and the decode alone would be expensive
// (every user row into Go on every redemption). Together they are exact
// and cheap.
//
// It is still an unindexed scan, and a deployment with a very large user
// table and a very hot redemption loop would want the same treatment
// user_groups got: rows instead of JSON. Nothing needs it yet.
//
// Accepted limitation, the same one GroupRecipients carries: fan-out
// reaches only users who have logged in at least once, holding the
// accounts they held at that login. The server never enumerates a
// directory to email strangers.
func (s *NotificationService) ServiceAccountRecipients(ctx context.Context, accountName string) ([]model.User, error) {
	if accountName == "" {
		return nil, nil
	}

	// The quotes are part of the pattern: they are what make this match a
	// whole element of the JSON array rather than any substring of one.
	quoted, err := json.Marshal(accountName)
	if err != nil {
		// not covered: json.Marshal cannot fail on a string.
		return nil, fmt.Errorf("failed to encode service account %q: %w", accountName, err)
	}

	var candidates []model.User
	err = s.db.WithContext(ctx).
		Where("service_accounts LIKE ?", "%"+string(quoted)+"%").
		// Disabled accounts are excluded, the same rule GroupRecipients
		// applies: a disable removes fan-out eligibility.
		Where("disabled_at IS NULL").
		Where("email <> ''").
		Find(&candidates).Error
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the holders of service account %q: %w", accountName, err)
	}

	holders := make([]model.User, 0, len(candidates))
	for _, user := range candidates {
		var accounts []string
		if err := json.Unmarshal([]byte(user.ServiceAccounts), &accounts); err != nil {
			// One unreadable row must not silence the notification for
			// everyone else holding the account.
			slog.WarnContext(ctx, "skipping a user whose service accounts do not parse",
				"user_id", user.ID, "error", err)
			continue
		}
		if slices.Contains(accounts, accountName) {
			holders = append(holders, user)
		}
	}
	return holders, nil
}
