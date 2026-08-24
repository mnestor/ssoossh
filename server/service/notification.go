package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	Notify(ctx context.Context, kind notify.Kind, userID string, payload any)
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
	if !s.enabled {
		return
	}
	if userID == "" {
		// Nothing to deliver to. Worth a log line rather than silence: it
		// means a calling path lost track of who it was acting for.
		slog.WarnContext(ctx, "skipping a notification with no recipient", "kind", kind)
		return
	}

	event, err := notify.NewEvent(kind, userID, payload)
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

// handle delivers one notification.
//
// The distinction that matters here is permanent versus temporary. A
// message that cannot be parsed, a kind this build does not know, a user
// who no longer exists or has no address, a preference that says no — none
// of those get better on redelivery, so they are acknowledged with a log
// line. A relay that would not take the message might, so that nacks and
// the router's retry middleware backs off and tries again.
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

	var user model.User
	if err := h.db.WithContext(ctx).First(&user, "id = ?", event.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			slog.WarnContext(ctx, "dropping a notification for a user that no longer exists",
				"kind", event.Kind, "user_id", event.UserID)
			return nil
		}
		return fmt.Errorf("failed to look up the notification recipient: %w", err)
	}
	if user.Email == "" {
		// Common rather than exceptional: an identity provider that does
		// not release an email claim leaves every user here.
		slog.DebugContext(ctx, "skipping a notification for a user with no address",
			"kind", event.Kind, "user_id", event.UserID)
		return nil
	}

	// Read at delivery, not at publication: an event that waited through a
	// retry backoff has to respect the answer the user gave in the
	// meantime.
	enabled, err := (&NotificationService{db: h.db}).enabledFor(ctx, event.UserID, event.Kind)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	rendered, err := h.renderer.Render(event.Kind, payload)
	if err != nil {
		// A template that does not render will not render on redelivery
		// either: every template is parsed and executed at startup, so
		// reaching here means the payload itself is the problem.
		slog.ErrorContext(ctx, "discarding a notification that could not be rendered",
			"kind", event.Kind, "error", err)
		return nil
	}

	if err := h.sender.Send(ctx, mail.Outgoing{To: user.Email, Rendered: rendered}); err != nil {
		return fmt.Errorf("failed to send the %s notification: %w", event.Kind, err)
	}

	slog.InfoContext(ctx, "notification sent", "kind", event.Kind, "user_id", event.UserID)
	return nil
}

// discardNotifications is the Notifier a service holds until bootstrap
// attaches a real one. Every certificate path can then call Notify
// unconditionally: a deployment with mail disabled, and every test that
// never wires notifications up, get a no-op instead of a nil check.
type discardNotifications struct{}

// Notify discards the notification.
func (discardNotifications) Notify(context.Context, notify.Kind, string, any) {}

// NotificationPreferenceProvider is the preferences controller's view of
// NotificationService: read and write the caller's own answers, nothing
// else. An interface so the HTTP contract can be tested without a database
// behind it.
type NotificationPreferenceProvider interface {
	PreferencesForIdentity(ctx context.Context, identity *Identity) (NotificationSettings, error)
	SetPreferencesForIdentity(ctx context.Context, identity *Identity, updates map[notify.Kind]bool) error
}
