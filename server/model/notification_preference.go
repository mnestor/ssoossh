package model

import "time"

// NotificationPreference is one user's answer for one notification kind.
//
// Stored as rows rather than as a column per kind, or a JSON blob on
// users, because the set of kinds grows: a new notify.Kind must not be a
// schema change, and it must not need a backfill either. A user with no row
// for a kind gets that kind's registered default (notify.DefaultEnabled),
// which is what makes "add a Definition and two templates" the whole cost
// of a new notification.
//
// The same shape is what lets a row survive a kind being removed or
// renamed: nothing joins against the registry, so an orphan row is inert
// rather than an error.
type NotificationPreference struct {
	ID string `gorm:"column:id;primaryKey"`

	// UserID is users.id. Preferences key off the stable user record, not
	// off the session identity, so they survive an address change at the
	// identity provider.
	UserID string `gorm:"column:user_id;uniqueIndex:idx_notification_preferences_user_kind"`

	// Kind is the notify.Kind string. Not constrained to the registry in
	// the schema on purpose — see the type comment.
	Kind string `gorm:"column:kind;uniqueIndex:idx_notification_preferences_user_kind"`

	// Enabled is the user's explicit choice. A row exists only once they
	// have made one; absence means the registered default.
	Enabled bool `gorm:"column:enabled"`

	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (NotificationPreference) TableName() string { return "notification_preferences" }
