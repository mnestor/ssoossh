package model

import "time"

// HostMapping is the server-side source of truth for principal mapping that
// `ssoossh host sync` pulls down and writes locally for sshd's
// AuthorizedPrincipalsCommand to answer from without touching the network
// (see docs/dev/ssoossh-context.md, "Principal mapping"). Purely local mapping
// files on the host remain supported independent of this table.
type HostMapping struct {
	ID       string `gorm:"column:id;primaryKey"`
	Hostname string `gorm:"column:hostname;uniqueIndex:idx_host_mappings_hostname"`

	// Principals is JSON-encoded; exact shape TODO (map of local account ->
	// certificate principals, or a flat list — depends on how `host sync`
	// and AuthorizedPrincipalsCommand end up consuming it).
	Principals string    `gorm:"column:principals"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (HostMapping) TableName() string { return "host_mappings" }
