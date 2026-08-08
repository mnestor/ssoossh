package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/mnestor/ssoossh/server/config"
)

// Identity is the resolved user identity after OIDC (+ optional LDAP)
// authentication. Groups are used only for the certificate lifetime
// decision — never placed in a certificate (see root CLAUDE.md Hard
// Constraints).
type Identity struct {
	Subject  string
	Username string
	Email    string
	Groups   []string
}

// AuthProvider handles OIDC login. AuthService is the production
// implementation.
type AuthProvider interface {
	// AuthorizationURL returns the URL to redirect the browser to for OIDC
	// login, and the nonce embedded in it. Both state and nonce must be
	// stored (e.g. in the session) and re-checked by HandleCallback.
	AuthorizationURL(ctx context.Context, state string) (authURL string, nonce string, err error)
	// HandleCallback exchanges code for tokens and verifies the ID token,
	// including that its nonce claim matches nonce.
	HandleCallback(ctx context.Context, code string, nonce string) (*Identity, error)
}

// AuthService handles OIDC authentication: building the authorization URL,
// exchanging the callback code, and mapping ID token claims (optionally
// enriched from LDAP per config.LDAPConfig) to an Identity.
//
// TODO: LDAP enrichment (config.LDAPConfig) and persisting/upserting the
// resulting identity as a model.User aren't implemented yet.
type AuthService struct {
	config       *config.Config
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	oauth2Config *oauth2.Config
}

// NewAuthService discovers the OIDC provider at c.HTTP.AuthConfig.ProviderURL
// (its authorization/token/jwks endpoints) and builds an AuthService ready
// to handle logins. httpClient (may be nil) is used for the discovery
// request and all subsequent calls to the provider.
func NewAuthService(ctx context.Context, c *config.Config, httpClient *http.Client) (*AuthService, error) {
	authConfig := c.HTTP.AuthConfig

	if authConfig.ProviderURL == "" {
		return nil, errors.New("http.authentication.provider_url is required")
	}
	if authConfig.ClientID == "" {
		return nil, errors.New("http.authentication.client_id is required")
	}
	if authConfig.RedirectURL == "" {
		return nil, errors.New("http.authentication.redirect_url is required")
	}
	if authConfig.Fields.Username == "" {
		return nil, errors.New("http.authentication.fields.username is required")
	}

	if httpClient != nil {
		ctx = oidc.ClientContext(ctx, httpClient)
	}

	provider, err := oidc.NewProvider(ctx, authConfig.ProviderURL)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC provider %q: %w", authConfig.ProviderURL, err)
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: authConfig.ClientID})

	scopes := []string{oidc.ScopeOpenID}
	scopes = append(scopes, strings.Fields(authConfig.Scopes)...)

	return &AuthService{
		config:   c,
		provider: provider,
		verifier: verifier,
		oauth2Config: &oauth2.Config{
			ClientID:     authConfig.ClientID,
			ClientSecret: authConfig.ClientSecret,
			RedirectURL:  authConfig.RedirectURL,
			Scopes:       scopes,
			Endpoint:     provider.Endpoint(),
		},
	}, nil
}

// AuthorizationURL returns the URL to redirect the browser to for OIDC
// login, embedding state (CSRF protection for the redirect, checked by the
// caller) and a freshly generated nonce (replay protection for the ID
// token, checked by HandleCallback).
func (s *AuthService) AuthorizationURL(ctx context.Context, state string) (authURL string, nonce string, err error) {
	nonce, err = randomToken()
	if err != nil {
		return "", "", fmt.Errorf("failed to generate OIDC nonce: %w", err)
	}

	return s.oauth2Config.AuthCodeURL(state, oidc.Nonce(nonce)), nonce, nil
}

// HandleCallback exchanges code for tokens, verifies the ID token (signature,
// audience, expiry, and that its nonce claim matches nonce), and extracts
// username/groups per config.OAuthFields.
func (s *AuthService) HandleCallback(ctx context.Context, code string, nonce string) (*Identity, error) {
	token, err := s.oauth2Config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange OIDC authorization code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("OIDC token response is missing id_token")
	}

	idToken, err := s.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to verify OIDC ID token: %w", err)
	}

	if idToken.Nonce != nonce {
		return nil, errors.New("OIDC ID token nonce does not match the one issued at login")
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to parse OIDC ID token claims: %w", err)
	}

	fields := s.config.HTTP.AuthConfig.Fields

	username, ok := claims[fields.Username].(string)
	if !ok || username == "" {
		return nil, fmt.Errorf("OIDC ID token is missing the configured username claim %q", fields.Username)
	}

	var groups []string
	if fields.Groups != "" {
		groupsClaim, ok := claims[fields.Groups].([]any)
		if !ok {
			return nil, fmt.Errorf("OIDC ID token is missing the configured groups claim %q", fields.Groups)
		}
		for _, g := range groupsClaim {
			if gs, ok := g.(string); ok {
				groups = append(groups, gs)
			}
		}
	}

	// email is a standard OIDC claim but not one of config.OAuthFields'
	// configurable mappings; take it opportunistically.
	var email string
	if e, ok := claims["email"].(string); ok {
		email = e
	}

	// TODO: enrich from LDAP when s.config.HTTP.LDAP.Enabled, and
	// upsert/load the corresponding model.User.

	return &Identity{
		Subject:  idToken.Subject,
		Username: username,
		Email:    email,
		Groups:   groups,
	}, nil
}

// randomToken returns a random, URL-safe string suitable for a one-time use
// value like an OIDC nonce.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
