package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Identity is the resolved user identity after OIDC (+ optional LDAP)
// authentication. Groups are used only for the certificate lifetime
// decision — never placed in a certificate (see root CLAUDE.md Hard
// Constraints). OtherAccounts and ServiceAccounts are persisted on
// model.User but not otherwise consumed yet.
type Identity struct {
	Subject         string
	Username        string
	Email           string
	Groups          []string
	OtherAccounts   []string
	ServiceAccounts []string
}

// AuthProvider handles OIDC login. AuthService is the production
// implementation.
type AuthProvider interface {
	// AuthorizationURL returns the URL to redirect the browser to for OIDC
	// login, and the nonce and PKCE verifier embedded in it. State, nonce,
	// and pkceVerifier must all be stored (e.g. in the session) and
	// re-checked by HandleCallback.
	AuthorizationURL(ctx context.Context, state string) (authURL string, nonce string, pkceVerifier string, err error)
	// HandleCallback exchanges code for tokens using the PKCE verifier and
	// verifies the ID token, including that its nonce claim matches nonce.
	HandleCallback(ctx context.Context, code string, nonce string, pkceVerifier string) (*Identity, error)
}

// AuthService handles OIDC authentication: building the authorization URL,
// exchanging the callback code, mapping ID token claims (optionally
// enriched from LDAP per config.LDAPConfig) to an Identity, and upserting
// the corresponding model.User.
//
// TODO: LDAP enrichment (config.LDAPConfig) isn't implemented yet.
type AuthService struct {
	config       *config.Config
	db           *gorm.DB
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	oauth2Config *oauth2.Config
}

// NewAuthService discovers the OIDC provider at c.AuthConfig.ProviderURL
// (its authorization/token/jwks endpoints) and builds an AuthService ready
// to handle logins. httpClient (may be nil) is used for the discovery
// request and all subsequent calls to the provider. The OAuth redirect URL
// is not itself configured — it's inferred from c.HTTP (ServerName, Port,
// and whether the server is HTTPS either directly or behind a
// TLS-terminating proxy — see IsHTTPS's doc comment), since everything
// after the domain is fixed anyway ("/auth/callback").
func NewAuthService(ctx context.Context, c *config.Config, db *gorm.DB, httpClient *http.Client) (*AuthService, error) {
	authConfig := c.AuthConfig

	if authConfig.ProviderURL == "" {
		return nil, errors.New("authentication.provider_url is required")
	}
	if authConfig.ClientID == "" {
		return nil, errors.New("authentication.client_id is required")
	}
	if authConfig.Fields.Username == "" {
		return nil, errors.New("authentication.fields.username is required")
	}
	if c.HTTP.ServerName == "" {
		return nil, errors.New("http.server_name is required")
	}

	if httpClient != nil {
		ctx = oidc.ClientContext(ctx, httpClient)
	}

	if strings.Contains(authConfig.ProviderURL, "/.well-known/openid-configuration") {
		authConfig.ProviderURL = authConfig.ProviderURL[:len(authConfig.ProviderURL)-33]
	}
	provider, err := oidc.NewProvider(ctx, authConfig.ProviderURL)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OIDC provider %q: %w", authConfig.ProviderURL, err)
	}

	verifier := provider.Verifier(&oidc.Config{ClientID: authConfig.ClientID})

	scopes := []string{oidc.ScopeOpenID}
	scopes = append(scopes, strings.Fields(authConfig.Scopes)...)

	// The identity provider matches this against its registered redirect URI
	// exactly, so it has to be the origin the *browser* uses. Behind a proxy
	// that is http.public_url; with nothing in front, PublicOrigin infers it
	// from server_name and the listen port.
	origin := c.HTTP.PublicOrigin()
	if origin == "" {
		// not covered: PublicOrigin only returns "" when ServerName is
		// also "", which the required-field check above already rejects.
		return nil, errors.New("cannot build the OIDC redirect URI: set http.public_url to the URL browsers reach this server at, e.g. \"https://ssh.example.com\" (or http.server_name when nothing sits in front of this process)")
	}
	redirectURL := origin + "/auth/callback"
	slog.Debug("oauth setting", slog.String("redirectURL", redirectURL))

	return &AuthService{
		config:   c,
		db:       db,
		provider: provider,
		verifier: verifier,
		oauth2Config: &oauth2.Config{
			ClientID:     authConfig.ClientID,
			ClientSecret: authConfig.ClientSecret,
			RedirectURL:  redirectURL,
			Scopes:       scopes,
			Endpoint:     provider.Endpoint(),
		},
	}, nil
}

// AuthorizationURL returns the URL to redirect the browser to for OIDC
// login, embedding state (CSRF protection for the redirect, checked by the
// caller), a freshly generated nonce (replay protection for the ID token,
// checked by HandleCallback), and a PKCE code challenge (checked by
// HandleCallback during code exchange).
func (s *AuthService) AuthorizationURL(ctx context.Context, state string) (authURL string, nonce string, pkceVerifier string, err error) {
	nonce, err = randomToken()
	if err != nil {
		// not covered: randomToken fails only if crypto/rand.Read does,
		// which crashes the process rather than returning an error.
		return "", "", "", fmt.Errorf("failed to generate OIDC nonce: %w", err)
	}

	pkceVerifier = oauth2.GenerateVerifier()

	return s.oauth2Config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(pkceVerifier)), nonce, pkceVerifier, nil
}

// HandleCallback exchanges code for tokens using the PKCE verifier, verifies
// the ID token (signature, audience, expiry, and that its nonce claim matches
// nonce), extracts identity fields per config.OAuthFields, and upserts the
// corresponding model.User.
func (s *AuthService) HandleCallback(ctx context.Context, code string, nonce string, pkceVerifier string) (*Identity, error) {
	token, err := s.oauth2Config.Exchange(ctx, code, oauth2.VerifierOption(pkceVerifier))
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
		// not covered: Verify above already parsed this same payload as
		// JSON to validate the standard claims, so re-unmarshaling it
		// here cannot fail.
		return nil, fmt.Errorf("failed to parse OIDC ID token claims: %w", err)
	}

	fields := s.config.AuthConfig.Fields

	username, ok := claims[fields.Username].(string)
	if !ok || username == "" {
		return nil, fmt.Errorf("OIDC ID token is missing the configured username claim %q", fields.Username)
	}

	// not covered (the three error branches below): all three calls pass
	// required=false, and stringSliceClaim only returns an error when
	// required is true. Otherwise it warns and returns nil.
	groups, err := stringSliceClaim(claims, fields.Groups, false)
	if err != nil {
		return nil, err
	}
	otherAccounts, err := stringSliceClaim(claims, fields.OtherAccounts, false)
	if err != nil {
		return nil, err
	}
	serviceAccounts, err := stringSliceClaim(claims, fields.ServiceAccounts, false)
	if err != nil {
		return nil, err
	}

	// email falls back to the standard "email" claim opportunistically if
	// no explicit mapping is configured; either way, absence isn't an error.
	emailField := fields.Email
	if emailField == "" {
		emailField = "email"
	}
	var email string
	if e, ok := claims[emailField].(string); ok {
		email = e
	}

	identity := &Identity{
		Subject:         idToken.Subject,
		Username:        username,
		Email:           email,
		Groups:          groups,
		OtherAccounts:   otherAccounts,
		ServiceAccounts: serviceAccounts,
	}

	if err := s.upsertUser(ctx, identity); err != nil {
		return nil, fmt.Errorf("failed to persist user: %w", err)
	}

	return identity, nil
}

// upsertUser creates or updates the model.User row for identity, keyed by
// Subject. Group membership is deliberately not persisted here (see root
// CLAUDE.md Hard Constraints).
func (s *AuthService) upsertUser(ctx context.Context, identity *Identity) error {
	// not covered (both error branches below): these are []string, so
	// json.Marshal cannot fail on them.
	otherAccountsJSON, err := json.Marshal(identity.OtherAccounts)
	if err != nil {
		return err
	}
	serviceAccountsJSON, err := json.Marshal(identity.ServiceAccounts)
	if err != nil {
		return err
	}

	now := time.Now()
	user := model.User{
		ID:              uuid.NewString(),
		Subject:         identity.Subject,
		Username:        identity.Username,
		Email:           identity.Email,
		OtherAccounts:   string(otherAccountsJSON),
		ServiceAccounts: string(serviceAccountsJSON),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "subject"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"username", "email", "other_accounts", "service_accounts", "updated_at",
		}),
	}).Create(&user).Error
}

// stringSliceClaim reads key from claims as a []string, returning nil (not
// an error) when key is empty (the field is unconfigured). It's an error
// for a configured key to be present-but-wrong-shaped, or absent entirely,
// since the operator explicitly asked for it.
func stringSliceClaim(claims map[string]any, key string, required bool) ([]string, error) {
	if key == "" {
		return nil, nil
	}

	raw, ok := claims[key].([]any)
	if !ok {
		if required {
			return nil, fmt.Errorf("OIDC ID token is missing the configured claim %q", key)
		}
		slog.Warn("OIDC ID token is missing the configurged claim", slog.String("claim", key))
		return nil, nil
	}

	values := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			values = append(values, s)
		}
	}
	return values, nil
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
