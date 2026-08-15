package model

import "time"

// ServerSecretSessionKey names the row holding the session cookie signing
// and encryption key.
const ServerSecretSessionKey = "session_cookie_key"

// ServerSecret holds key material the server generates for itself when the
// operator has not configured it, so it survives a restart instead of being
// regenerated per process.
//
// Deliberately not a general settings table. Server configuration comes from
// the config file only and is never reconfigurable at runtime; this exists
// solely for generated secrets, which have no meaningful representation in a
// config file the operator writes by hand.
type ServerSecret struct {
	// Name identifies the secret, e.g. ServerSecretSessionKey.
	Name string `gorm:"column:name;primaryKey"`

	// Value is raw key material, not encoded. Rows here are as sensitive as
	// the database itself.
	Value     []byte    `gorm:"column:value"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (ServerSecret) TableName() string { return "server_secrets" }
