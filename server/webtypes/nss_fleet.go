package webtypes

import (
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

// The NSS directory's session-authenticated shapes: the fleet API key
// administration under /api/nss/keys. The host-facing lookups are in
// internal/apitypes, because the ssoosshd-nss daemon decodes them in Go.

// NSSKeyResponse is one fleet API key. The secret is never part of it:
// only NSSKeySecretResponse carries one, once.
type NSSKeyResponse struct {
	// ID is "nss_" and 8 base32 characters, what hosts send as
	// X-Ssoossh-Key-Id. Fixed for the key's life.
	ID          string `json:"id" validate:"required"`
	Name        string `json:"name" validate:"required"`
	Description string `json:"description" validate:"required"`
	// GroupName is the group whose members manage this key, empty for
	// none.
	GroupName string `json:"group_name" validate:"required"`
	// Status is derived from the revocation and expiry.
	Status model.NSSKeyStatus `json:"status" validate:"required"`
	// CurrentGeneration is 1 at creation and grows by one per rotation.
	CurrentGeneration int `json:"current_generation" validate:"required"`
	// CreatedBy is the creator's users.id, and CreatedByUsername their
	// username, empty when the account is gone.
	CreatedBy         string    `json:"created_by" validate:"required"`
	CreatedByUsername string    `json:"created_by_username" validate:"required"`
	CreatedAt         time.Time `json:"created_at" validate:"required"`
	UpdatedAt         time.Time `json:"updated_at" validate:"required"`
	// ExpiresAt is when the key stops being accepted, set at creation and
	// renewed by each rotation; absent for a key that never expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// NotificationEmail is where the key's mail goes, empty for nowhere.
	NotificationEmail string `json:"notification_email" validate:"required"`
	// RotatedAt is the last rotation, absent for a key never rotated.
	RotatedAt *time.Time `json:"rotated_at,omitempty"`
	// PreviousExpiresAt is when the previous secret stops being accepted,
	// absent when there is no previous secret.
	PreviousExpiresAt *time.Time `json:"previous_expires_at,omitempty"`
	// RevokedAt, RevokedBy (a users.id) and RevokedByUsername record a
	// revocation, absent while the key is live.
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	RevokedBy         *string    `json:"revoked_by,omitempty"`
	RevokedByUsername string     `json:"revoked_by_username,omitempty"`
	// Permissions is what the caller may do with this key, decided by the
	// server so the UI never reimplements the role matrix. A revoked key
	// carries view alone.
	Permissions []model.NSSKeyPermission `json:"permissions" validate:"required"`
	// LastUsedAt is the last lookup any host made with the key, from the
	// retained usage, so up to a minute behind; absent for a key no host
	// has used.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// MissRateAlertAt is when the miss rate alert (nss.miss_alert_rate)
	// last fired for the key: many lookups for names the directory does
	// not hold. Absent unless it fired in the last 7 days.
	MissRateAlertAt *time.Time `json:"miss_rate_alert_at,omitempty"`
	// KeySourceAlertAt is when the key source alert
	// (nss.key_source_alert) last fired for the key: more source
	// addresses in one day than the limit. Absent unless it fired in the
	// last 7 days.
	KeySourceAlertAt *time.Time `json:"key_source_alert_at,omitempty"`
}

// NSSKeySettingsResponse is GET /api/nss/key-settings: the parts of the
// nss configuration the key pages state before an operator acts, so the
// create form can bound and propose an expiry and the rotate dialog can
// name the day the previous secret stops working.
type NSSKeySettingsResponse struct {
	// MaxLifetimeSeconds is nss.key_max_lifetime: the latest a new key
	// may expire, and its expiry when none is given. 0 sets no limit, and
	// a key created without a date then never expires.
	MaxLifetimeSeconds int `json:"max_lifetime_seconds" validate:"required"`
	// RotationGraceSeconds is nss.key_rotation_grace: how long the
	// previous secret stays accepted after a rotation.
	RotationGraceSeconds int `json:"rotation_grace_seconds" validate:"required"`
}

// NSSKeyListResponse is GET /api/nss/keys: every key the caller may view,
// by name. Keys are fleet-sized, so the list is not paged.
type NSSKeyListResponse struct {
	Keys []NSSKeyResponse `json:"keys" validate:"required"`
	// CanCreate is whether the caller may create keys.
	CanCreate bool `json:"can_create" validate:"required"`
}

// NSSKeySecretResponse is the answer to creating or rotating a key: the
// key, and its new secret, shown this once and never again. The field is
// named secret so the client trace redacts it.
type NSSKeySecretResponse struct {
	Key    NSSKeyResponse `json:"key" validate:"required"`
	Secret string         `json:"secret" validate:"required"`
}

// CreateNSSKeyBody creates a key. ExpiresAt defaults to now plus
// nss.key_max_lifetime, and a later one is trimmed to that; with the
// setting at 0 a key without one never expires. Afterwards only a
// rotation renews it. NotificationEmail defaults to the creator's
// address; an empty one sends the key's mail nowhere.
type CreateNSSKeyBody struct {
	Name              string     `json:"name" validate:"required"`
	Description       string     `json:"description,omitempty"`
	GroupName         string     `json:"group_name,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	NotificationEmail *string    `json:"notification_email,omitempty"`
}

// UpdateNSSKeyBody changes a key; fields left out are unchanged.
// GroupName needs the fleet key admin role, the rest the key's own group.
// The expiry is not here: only a rotation renews it.
type UpdateNSSKeyBody struct {
	Description       *string `json:"description,omitempty"`
	NotificationEmail *string `json:"notification_email,omitempty"`
	GroupName         *string `json:"group_name,omitempty"`
}

// NSSKeyUsageRowResponse is one key's lookups from one address with one
// generation of its secret on one day.
type NSSKeyUsageRowResponse struct {
	SourceIP   string `json:"source_ip" validate:"required"`
	Generation int    `json:"generation" validate:"required"`
	// Day is midnight UTC of the day counted.
	Day       time.Time `json:"day" validate:"required"`
	Lookups   int64     `json:"lookups" validate:"required"`
	Misses    int64     `json:"misses" validate:"required"`
	FirstSeen time.Time `json:"first_seen" validate:"required"`
	LastSeen  time.Time `json:"last_seen" validate:"required"`
}

// NSSKeyUsageResponse is one page of GET /api/nss/keys/{id}/usage, newest
// day first.
type NSSKeyUsageResponse struct {
	Rows []NSSKeyUsageRowResponse `json:"rows" validate:"required"`
	// Total counts every retained row for the key, across all pages.
	Total int64 `json:"total" validate:"required"`
	// NextCursor is the after for the following page, absent on the last.
	NextCursor *string `json:"next_cursor,omitempty"`
}

// NSSRotationReportRowResponse is one source address in the rotation
// report. FirstSeen, LastSeen and Lookups cover every retained usage row.
type NSSRotationReportRowResponse struct {
	SourceIP       string    `json:"source_ip" validate:"required"`
	FirstSeen      time.Time `json:"first_seen" validate:"required"`
	LastSeen       time.Time `json:"last_seen" validate:"required"`
	Lookups        int64     `json:"lookups" validate:"required"`
	LastGeneration int       `json:"last_generation" validate:"required"`
	// Bucket is switched (last presented the current secret), not_switched
	// (last presented an older one) or gone (not seen since the rotation).
	Bucket model.NSSRotationBucket `json:"bucket" validate:"required"`
}

// NSSRotationReportResponse is GET /api/nss/keys/{id}/rotation-report:
// which addresses have switched to the current secret since the last
// rotation, not_switched first.
type NSSRotationReportResponse struct {
	KeyID             string     `json:"key_id" validate:"required"`
	CurrentGeneration int        `json:"current_generation" validate:"required"`
	RotatedAt         *time.Time `json:"rotated_at,omitempty"`
	PreviousExpiresAt *time.Time `json:"previous_expires_at,omitempty"`
	// GraceDaysLeft is whole days, rounded up, until the previous secret
	// stops working; zero when there is none or it has ended.
	GraceDaysLeft int                            `json:"grace_days_left" validate:"required"`
	Switched      int                            `json:"switched" validate:"required"`
	NotSwitched   int                            `json:"not_switched" validate:"required"`
	Gone          int                            `json:"gone" validate:"required"`
	Rows          []NSSRotationReportRowResponse `json:"rows" validate:"required"`
}
