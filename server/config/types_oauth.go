package config

// OAuthConfig configures the OIDC provider used to authenticate users.
type OAuthConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	ProviderURL  string `mapstructure:"provider_url"`

	// Scopes is a space-separated list of additional scopes to request
	// alongside the always-included "openid" scope, e.g. "profile email".
	Scopes string      `mapstructure:"scopes"`
	Fields OAuthFields `mapstructure:"fields"`
}

// OAuthFields maps ssoossh identity fields to claim names in the OIDC
// provider's ID token. Username is the only required field; the rest are
// empty by default, meaning "not populated from OIDC".
type OAuthFields struct {
	Username string `mapstructure:"username"`

	// Groups, OtherAccounts, and ServiceAccounts all name a claim expected
	// to hold a JSON array of strings, parsed the same way. Groups feeds
	// the certificate lifetime decision only (never placed in a
	// certificate, see root CLAUDE.md Hard Constraints). OtherAccounts are
	// alternate account identifiers this identity is known by on target
	// systems (e.g. a different username, UPN, or sAMAccountName),
	// intended to be added to a certificate's principal list alongside
	// Username. ServiceAccounts are service-account identifiers this
	// identity is authorized to manage/enroll certificates for.
	Groups          string `mapstructure:"groups"`
	OtherAccounts   string `mapstructure:"other_accounts"`
	ServiceAccounts string `mapstructure:"service_accounts"`

	// Email names the claim to read the user's email from. Empty falls
	// back to the standard "email" claim opportunistically (not an error
	// if absent either way).
	Email string `mapstructure:"email"`

	// Extra maps additional template field names to claim names. Each
	// configured claim is captured at login (scalars as strings, string
	// arrays as lists), stored on the users row, and made available to
	// key ID templates as {{.Extra.<name>}} (or {{join .Extra.<name>
	// "<sep>"}} for lists). A claim absent from the ID token stores empty
	// and renders as MISSING; login never fails over one. Names with
	// characters beyond letters/digits/underscores need the template's
	// index syntax: {{index .Extra "my-name"}}.
	Extra map[string]string `mapstructure:"extra"`
}
