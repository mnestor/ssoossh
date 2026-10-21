package model

import "time"

// UserInfoHealthID is the one row's id: there is one UserInfo endpoint.
const UserInfoHealthID = "userinfo"

// UserInfoHealth is the state of the OIDC UserInfo endpoint
// (authentication.userinfo), shared by every instance through the
// database so that failures count across the fleet and a pause holds on
// every instance at once. Never holds a token or a claim.
type UserInfoHealth struct {
	ID string `gorm:"column:id;primaryKey"`

	// ConsecutiveFailures counts failed calls since the last success.
	ConsecutiveFailures int `gorm:"column:consecutive_failures"`
	// LastError is the last failed call's error, the access token
	// scrubbed out.
	LastError     string     `gorm:"column:last_error"`
	LastSuccessAt *time.Time `gorm:"column:last_success_at"`
	LastFailureAt *time.Time `gorm:"column:last_failure_at"`

	// Paused is set when the failure policy stopped the calls, at
	// PausedAt: logins then use the ID token alone, except for one probe
	// call per window. NextProbeAt is when the next probe may run; the
	// instance whose conditional update moves it forward is the one that
	// probes. A successful probe clears all three.
	Paused      bool       `gorm:"column:paused"`
	PausedAt    *time.Time `gorm:"column:paused_at"`
	NextProbeAt *time.Time `gorm:"column:next_probe_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (UserInfoHealth) TableName() string { return "oidc_userinfo_health" }
