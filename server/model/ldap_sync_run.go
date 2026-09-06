package model

import "time"

// LDAPSyncTrigger says what started a directory sync pass.
type LDAPSyncTrigger string

const (
	// LDAPSyncTriggerSchedule is the background job on ldap.sync.interval.
	LDAPSyncTriggerSchedule LDAPSyncTrigger = "schedule"
	// LDAPSyncTriggerManual is an admin pressing the button. The pass
	// itself is identical — a manual sync that behaved differently from a
	// scheduled one would be a diagnostic that lies.
	LDAPSyncTriggerManual LDAPSyncTrigger = "manual"
)

// LDAPSyncRun is one directory sync pass and what it concluded.
//
// It exists because Sync used to report only to the log: there was nowhere
// for a UI to read the outcome from, and no way to tell a sync that ran from
// one that never fired. It is also what a manual sync reports through, and
// what a dry run produces instead of changes.
type LDAPSyncRun struct {
	ID string `gorm:"column:id;primaryKey"`

	StartedAt time.Time `gorm:"column:started_at"`
	// FinishedAt is NULL while the pass runs, which is also how a pass that
	// died with its process reads afterwards.
	FinishedAt *time.Time `gorm:"column:finished_at"`

	Trigger LDAPSyncTrigger `gorm:"column:trigger_source"`

	// DryRun reports the directory without changing anything: no refreshed
	// attributes, no group rows, no miss windows, no disables. The counts
	// are what the pass would have done.
	DryRun bool `gorm:"column:dry_run"`

	// ActorUserID is the admin who triggered it, NULL for a scheduled pass.
	ActorUserID *string `gorm:"column:actor_user_id"`

	// Instance is the host that ran it. Jobs are not leader-elected, so
	// every instance runs its own pass and this is what says which log to
	// go and read.
	Instance string `gorm:"column:instance"`

	// UsersSeen is how many bookkeeping rows the pass considered; the rest
	// are what it concluded about them. Found, Missing and Failed sum to
	// UsersSeen.
	UsersSeen int `gorm:"column:users_seen"`
	Found     int `gorm:"column:found"`
	Missing   int `gorm:"column:missing"`
	Failed    int `gorm:"column:failed"`

	// Disabled and Reenabled count the containment actions the pass took —
	// or, on a dry run, would have taken.
	Disabled  int `gorm:"column:disabled"`
	Reenabled int `gorm:"column:reenabled"`

	// ErrorMessage is why the pass could not run: an unreachable directory,
	// a failed bind. Empty for a pass that completed, whatever it concluded
	// about individual users.
	ErrorMessage string `gorm:"column:error_message"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (LDAPSyncRun) TableName() string { return "ldap_sync_runs" }
