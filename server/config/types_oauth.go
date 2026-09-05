package config

// OAuthConfig configures the OIDC provider used to authenticate users.
type OAuthConfig struct {
	// ClientID is the OAuth2/OIDC client ID registered with the identity
	// provider. Required.
	ClientID string `mapstructure:"client_id" default:""`

	// ClientSecret is the OAuth2 client secret.
	ClientSecret string `mapstructure:"client_secret" default:"" secret:"true"`

	// ProviderURL is the OIDC provider's issuer URL, used for discovery. A
	// trailing /.well-known/openid-configuration is stripped automatically.
	// Required.
	ProviderURL string `mapstructure:"provider_url" default:""`

	// Scopes is a space-separated list of additional scopes to request
	// alongside the always-included "openid" scope, e.g. "profile email".
	Scopes string      `mapstructure:"scopes" default:"profile email"`
	Fields OAuthFields `mapstructure:"fields"`
}

// OAuthFields maps ssoossh identity fields to claim names in the OIDC
// provider's ID token. Username is the only required field; the rest are
// empty by default, meaning "not populated from OIDC".
type OAuthFields struct {
	// Username names the claim holding the local account username, e.g.
	// "preferred_username". Required.
	Username string `mapstructure:"username" default:"preferred_username"`

	// Groups names a claim expected to hold a JSON array of group names. It
	// feeds the certificate lifetime and require-group decision only; group
	// membership is never placed in an issued certificate (see
	// https://mnestor.github.io/ssoossh/internals/invariants/).
	Groups string `mapstructure:"groups" default:"groups"`

	// OtherAccounts names a claim expected to hold a JSON array of alternate
	// account identifiers this identity is known by on target systems (a
	// different username, UPN, or sAMAccountName), added to a certificate's
	// principal list alongside the username claim.
	OtherAccounts string `mapstructure:"other_accounts" default:""`

	// ServiceAccounts names a claim expected to hold a JSON array of
	// service-account identifiers this identity is authorized to manage and
	// enroll certificates for.
	ServiceAccounts string `mapstructure:"service_accounts" default:""`

	// Email names the claim to read the user's email from. Empty falls
	// back to the standard "email" claim opportunistically (not an error
	// if absent either way).
	Email string `mapstructure:"email" default:"email"`

	// Extra maps additional template field names to claim names. Each
	// configured claim is captured at login (scalars as strings, string
	// arrays as lists), stored on the users row, and made available to
	// key ID templates as {{.Extra.<name>}} (or {{join .Extra.<name>
	// "<sep>"}} for lists). A claim absent from the ID token stores empty
	// and renders as MISSING; login never fails over one. Names with
	// characters beyond letters/digits/underscores need the template's
	// index syntax: {{index .Extra "my-name"}}.
	//
	// For example, capturing a department claim and an account list:
	//
	//	authentication:
	//	  fields:
	//	    extra:
	//	      dept: "https://idp.example.com/department"
	//	      accounts: altAccounts
	//
	// then interpolated into a key ID:
	//
	//	cert_options:
	//	  user:
	//	    key_id_template: '{{.Username}}-{{.Extra.dept}}-{{join .Extra.accounts ";"}}'
	//
	// See https://mnestor.github.io/ssoossh/operations/key-id-templates/ for the full
	// template field list and the MISSING rendering rules.
	Extra map[string]string `mapstructure:"extra"`
}
