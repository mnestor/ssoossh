package model

import "time"

// IdentityEchoCode is one bootstrap identity echo link, minted on the server
// host by `ssoosshd identity echo` and redeemed once by the browser that
// opens it (see docs/proposals/identity-bootstrap-echo.md).
//
// The row holds the code's hash, never the code, and never any claim: the
// echoed claims reach the page in the redirect fragment and nowhere else.
// What it does hold is what the waiting command reports: when and from
// where the link was used, and how the sign-in that followed ended.
type IdentityEchoCode struct {
	// ID is a random id, the one the audit events name.
	ID string `gorm:"column:id;primaryKey"`

	// CodeHash is the hex SHA-256 of the code. A code carries 128 random
	// bits, so a plain hash is enough: there is nothing to brute-force.
	CodeHash string `gorm:"column:code_hash"`

	// RequestedBy says who minted it, as the host told the command: the
	// operating-system account and the hostname. Context, not proof.
	RequestedBy string `gorm:"column:requested_by"`

	CreatedAt time.Time `gorm:"column:created_at"`
	ExpiresAt time.Time `gorm:"column:expires_at"`

	// UsedAt is when a browser redeemed the code, and UsedFrom the address
	// the server observed for it (http.trusted_proxies honoured). Nil and
	// empty until then.
	UsedAt   *time.Time `gorm:"column:used_at"`
	UsedFrom string     `gorm:"column:used_from"`

	// CompletedAt is when the sign-in that followed came back to the
	// callback, and Outcome what it ended in: empty for verified claims,
	// otherwise the error that came before any claims (a refused code
	// exchange, a token that did not verify). Never a claim value.
	CompletedAt *time.Time `gorm:"column:completed_at"`
	Outcome     string     `gorm:"column:outcome"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (IdentityEchoCode) TableName() string { return "identity_echo_codes" }
