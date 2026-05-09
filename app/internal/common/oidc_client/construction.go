package oidc_client

import (
	"context"

	"github.com/coryschwartz/gin-oidc-client/crypt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type OauthHandlers struct {
	oauthLogoutUrl     string
	oauthRevocationUrl string
	oauthPKCESecret    string
	oauthSessionName   string
	oauth2config       *oauth2.Config
	verifier           *oidc.IDTokenVerifier
	oidcProvider       *oidc.Provider
	decrypter          crypt.TokenDecrypter
}

// OauthHandlers holds the configuration used by an Oauth/Oidc Client.
// oidcProvider:
//
//	This is the oidc discovery URL. Your OIDC provider shuold inform you what this URL should be.
//	The client will pull a configuration file at <oidcProvider>/.well-known/openid-configuration.
//
// oauthClientID:
//
//	This is a unique identifier representing account this application has with your oauth provider.
//
// oauthClientSecret:
//
//	Your application's password with the oauth provider. This is secret. Don't give it to anyone.
//
// oauthRedirectUrl:
//
//	This is sometiems also called the "callback" url. When you register your application with
//	the oauth provider, you will have to inform them what the redirect URL is, and we need to pass
//	the same URL. It should be set to the URL where users can connect to your application where the
//	HandleRedirect handler is listening.
//
// oauthScopes:
//
//	A list of scopes we should request the auth server to send us. Just because you request it, doesn't
//	mean the auth server will oblige. Your oauth server documentation will tell you what scopes are
//	available. When you add additional scopes, the auth server will return additional claims
//	during token exchange. The content of the claims are various. If you use the MiddlewareRequireLogin,
//	you will find the claims are made available as a gin context value.
// 	by default, the openid scope is added to the list of scopes.

func NewOauthHandlers(
	oidcProvider,
	oauthClientID,
	oauthClientSecret,
	oauthRedirectUrl string,
	oauthScopes []string,
	addlOptions ...OauthHandlersOption) (*OauthHandlers, error) {

	// default options
	opts := []OauthHandlersOption{
		WithOauthSessionName("login"),              // name of the gin-contrib/sessions session where information is stored.
		WithOauthPKCESecret(oauthClientSecret),     // pkce state encryption, same as the client secret by default.
		WithTokenDecrypter(&crypt.NullDecrypter{}), // no JWE decryption by default
	}

	// connect to the OIDC provider and get some information from it.
	// This does a network call to the discovery url.
	provider, err := oidc.NewProvider(context.Background(), oidcProvider)
	if err != nil {
		return nil, err
	}
	opts = append(opts, WithOidcProvider(provider))
	claims := make(map[string]any)
	provider.Claims(&claims)

	// maybe there is a logout endpoint.
	oauthLogoutUrl := "/"
	if val, ok := claims["end_session_endpoint"].(string); ok {
		oauthLogoutUrl = val
	}
	opts = append(opts, WithOauthLogoutUrl(oauthLogoutUrl))

	// maybe there is a revocation endpoint.
	tokenRevocationUrl := ""
	if val, ok := claims["revocation_endpoint"].(string); ok {
		tokenRevocationUrl = val
	}
	opts = append(opts, WithOauthRevocationUrl(tokenRevocationUrl))

	// oauth
	oauth2config := &oauth2.Config{
		ClientID:     oauthClientID,
		ClientSecret: oauthClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  oauthRedirectUrl,
		Scopes:       append([]string{oidc.ScopeOpenID}, oauthScopes...),
	}
	opts = append(opts, WithOauth2Config(oauth2config))

	// oidc
	verifier := provider.Verifier(&oidc.Config{
		ClientID: oauth2config.ClientID,
	})
	opts = append(opts, WithVerifier(verifier))

	return NewOauthHandlersWithOptions(append(opts, addlOptions...)...)
}

// NewOauthHandlersWithOptions creates a new OauthHandlers with the provided options.
// If you use this function, be aware that some options are required for proper functionality.
func NewOauthHandlersWithOptions(opts ...OauthHandlersOption) (*OauthHandlers, error) {
	oh := new(OauthHandlers)
	for _, opt := range opts {
		if err := opt(oh); err != nil {
			return nil, err
		}
	}
	return oh, nil
}

type OauthHandlersOption (func(*OauthHandlers) error)

// WithOauthLogoutURL
// Where should users go after they log out? Check if your oauth provider has a logout endpoint.
func WithOauthLogoutUrl(logoutUrl string) OauthHandlersOption {
	return func(oh *OauthHandlers) error {
		oh.oauthLogoutUrl = logoutUrl
		return nil
	}
}

// WithOauthRevocationUrl
// What URL should we use to revoke access tokens? Check if your oauth provider has a revocation endpoint.
func WithOauthRevocationUrl(revocationUrl string) OauthHandlersOption {
	return func(oh *OauthHandlers) error {
		oh.oauthRevocationUrl = revocationUrl
		return nil
	}
}

// WithOauthPKCESecret
// PKCE (RFC7636) is a good security practice. The general idea is that in the first Oauth phase,
// we pass a hash sum to the auth server. Then, when we do token exchange we pass the original data.
// The auth server can then know that both requests came from the same place. Generally, you have to
// store this data somehow. In this implementation, we are stuffing this data into the state and
// encrypting it. This is the encryption key. If not set, encryption may include
func WithOauthPKCESecret(pkcesecret string) OauthHandlersOption {
	return func(oh *OauthHandlers) error {
		oh.oauthPKCESecret = pkcesecret
		return nil
	}
}

// WithOauthSessionName
// We're using gin-contrib/sessions to store the oidc idtoken and other data to the session. You should
// create a session using whatever storage medium you wish. Use encryption if able. Use this variable
// to tell us which session you want login information to be stored.
func WithOauthSessionName(sessionName string) OauthHandlersOption {
	return func(oh *OauthHandlers) error {
		oh.oauthSessionName = sessionName
		return nil
	}
}

// WithOauth2Config
// Provide your own oauth2.Config
// If you use this option, you will probably also want to use
// WithVerifier and WithOidcProvider options.
func WithOauth2Config(oauth2config *oauth2.Config) OauthHandlersOption {
	return func(oh *OauthHandlers) error {
		oh.oauth2config = oauth2config
		return nil
	}
}

// WithVerifier
// Provide your own oidc.IDTokenVerifier.
// If you use this option, you will probably also want to use
// WithOauth2Config and WithOidcProvider options.
func WithVerifier(verifier *oidc.IDTokenVerifier) OauthHandlersOption {
	return func(oh *OauthHandlers) error {
		oh.verifier = verifier
		return nil
	}
}

// WithOidcProvider
// Provide your own oidc.Provider
// If you use this option, you will probably also want to use
// WithOauth2Config and WithVerifier options.
func WithOidcProvider(oidcProvider *oidc.Provider) OauthHandlersOption {
	return func(oh *OauthHandlers) error {
		oh.oidcProvider = oidcProvider
		return nil
	}
}

// WithTokenDecrypter
// Provide your own crypt.TokenDecrypter to decrypt JWE tokens
func WithTokenDecrypter(decrypter crypt.TokenDecrypter) OauthHandlersOption {
	return func(oh *OauthHandlers) error {
		oh.decrypter = decrypter
		return nil
	}
}
