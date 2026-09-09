package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// Identity is the resolved user identity after OIDC (+ optional LDAP)
// authentication. Groups are used only for the certificate lifetime decision
// — never placed in a certificate (see
// https://mnestor.github.io/ssoossh/internals/invariants/ Hard Constraints).
// OtherAccounts and ServiceAccounts are persisted on model.User and ride the
// login session (middleware.SetIdentitySession): ServiceAccounts gate
// service-approval linkage (checkServiceAccountLinkage) and both are surfaced
// by /api/users/me for the web UI's account page.
type Identity struct {
	Subject  string
	Username string
	Email    string

	// DisplayName is the person's human-readable name, from the claim named
	// by config.OAuthFields.Name and overridden by the directory's
	// config.LDAPFieldName. Display only: the web UI shows it beside the
	// username and email templates may render it. It is deliberately not a
	// principal candidate, not a key ID field, and never an authorization
	// input — a display name is not unique and nothing may act on it.
	DisplayName string

	Groups          []string
	OtherAccounts   []string
	ServiceAccounts []string

	// Extra holds the operator-configured extra claim fields (see
	// config.OAuthFields.Extra), keyed by template name. Populated from ID
	// token claims at login and persisted on model.User.ExtraFields; the
	// approval path re-hydrates it from that row (the session does not
	// carry it). Consumed by key ID templates as {{.Extra.name}}.
	Extra map[string]extraValue
}

// AuthProvider handles OIDC login. AuthService is the production
// implementation.
type AuthProvider interface {
	// AuthorizationURL returns the URL to redirect the browser to for OIDC
	// login, and the nonce and PKCE verifier embedded in it. State, nonce,
	// and pkceVerifier must all be stored (e.g. in the session) and
	// re-checked by HandleCallback. pkceVerifier is empty when PKCE is
	// disabled (authentication.disable_pkce), and the callback must pass
	// back whatever it was given either way.
	AuthorizationURL(ctx context.Context, state string) (authURL string, nonce string, pkceVerifier string, err error)
	// HandleCallback exchanges code for tokens using the PKCE verifier and
	// verifies the ID token, including that its nonce claim matches nonce.
	HandleCallback(ctx context.Context, code string, nonce string, pkceVerifier string) (*Identity, error)

	// EchoAuthorizationURL is AuthorizationURL with prompt=login, for the
	// claims echo: the caller is already signed in, and the point is to
	// see a fresh token rather than to reuse the provider's session.
	EchoAuthorizationURL(ctx context.Context, state string) (authURL string, nonce string, pkceVerifier string, err error)

	// EchoCallback verifies the callback the same way HandleCallback does
	// and returns the decoded ID token claims, establishing nothing: no
	// session, no user row, no login event.
	EchoCallback(ctx context.Context, code string, nonce string, pkceVerifier string) (map[string]any, error)

	// ClaimMapping reports which configured field consumes each claim, so
	// an echo can be annotated against the configuration rather than
	// printed raw.
	ClaimMapping() ClaimMapping
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
	// auditor records auth.login and auth.login_denied. Set after
	// construction (see SetAuditor) because it is a peer service, and left
	// nil in tests that do not exercise auditing.
	auditor *AuditService

	// ldap enriches the identity from the directory. Nil when LDAP is
	// disabled, which is the common case and needs no branch at the call
	// site beyond the nil check inside Enrich.
	ldap *LDAPService
}

// SetLDAP wires directory enrichment. Called once at startup.
func (s *AuthService) SetLDAP(l *LDAPService) { s.ldap = l }

// SetAuditor wires the audit recorder. Called once at startup, before any
// request is served.
func (s *AuthService) SetAuditor(a *AuditService) { s.auditor = a }

// audit records one event when an auditor is wired, so every call site can
// stay a single unconditional line.
func (s *AuthService) audit(ctx context.Context, event AuditEvent) {
	if s.auditor == nil {
		return
	}
	s.auditor.Record(ctx, event)
}

// NewAuthService discovers the OIDC provider at c.AuthConfig.ProviderURL
// (its authorization/token/jwks endpoints) and builds an AuthService ready
// to handle logins. httpClient (may be nil) is used for the discovery
// request and all subsequent calls to the provider. The OAuth redirect URL
// is not itself configured — it's derived from c.HTTP.PublicURL (see
// HTTPSettings.PublicOrigin), since everything after the origin is fixed
// anyway ("/auth/callback").
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
	// Empty would leave every user row keyed by the empty string, which is
	// one shared account rather than none. Rejected at startup rather than
	// defaulted here, so an operator who blanked it hears about it.
	if authConfig.Fields.Subject == "" {
		return nil, errors.New(`authentication.fields.subject is required: it names the claim holding the unique account identifier, and defaults to "sub"`)
	}
	if strings.TrimSpace(c.HTTP.PublicURL) == "" {
		return nil, errors.New("http.public_url is required: set it to the URL browsers reach this server at, e.g. \"https://ssh.example.com\"")
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
	// exactly, so it has to be the origin the *browser* uses, which is
	// http.public_url.
	origin := c.HTTP.PublicOrigin()
	if origin == "" {
		// not covered: PublicOrigin only returns "" when PublicURL is unset
		// (rejected above) or unparseable (rejected at startup by
		// HTTPSettings.Validate).
		return nil, errors.New("cannot build the OIDC redirect URI: set http.public_url to the URL browsers reach this server at, e.g. \"https://ssh.example.com\"")
	}
	redirectURL := origin + "/auth/callback"
	slog.Debug("oauth setting", slog.String("redirectURL", redirectURL))

	// Warned rather than refused, for the same reason
	// mail.smtp.insecure_skip_verify is: an operator whose provider rejects
	// code_challenge has no other way in. Said once, at startup, so a login
	// flow without PKCE is a choice someone made rather than one they
	// drifted into.
	if authConfig.DisablePKCE {
		slog.Warn("PKCE is disabled for OIDC login: an authorization code observed in transit can be redeemed by anyone holding it; unset authentication.disable_pkce once the provider accepts a code challenge",
			"authentication.disable_pkce", true, "authentication.provider_url", authConfig.ProviderURL)
	}

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
// checked by HandleCallback), and — unless authentication.disable_pkce is
// set — a PKCE code challenge (checked by HandleCallback during code
// exchange). The returned verifier is empty when PKCE is off.
func (s *AuthService) AuthorizationURL(ctx context.Context, state string) (authURL string, nonce string, pkceVerifier string, err error) {
	nonce, err = randomToken()
	if err != nil {
		// not covered: randomToken fails only if crypto/rand.Read does,
		// which crashes the process rather than returning an error.
		return "", "", "", fmt.Errorf("failed to generate OIDC nonce: %w", err)
	}

	opts, pkceVerifier := s.authCodeOptions(nonce)

	return s.oauth2Config.AuthCodeURL(state, opts...), nonce, pkceVerifier, nil
}

// authCodeOptions builds the options both authorization URLs share: the
// nonce, and a PKCE code challenge unless authentication.disable_pkce is
// set. The verifier it returns is empty in that case, and an empty verifier
// is what tells exchangeOptions to leave code_verifier off the exchange —
// so the two halves of one login can never disagree about PKCE even if the
// configuration changes between them.
func (s *AuthService) authCodeOptions(nonce string) (opts []oauth2.AuthCodeOption, pkceVerifier string) {
	opts = []oauth2.AuthCodeOption{oidc.Nonce(nonce)}
	if s.config.AuthConfig.DisablePKCE {
		return opts, ""
	}

	pkceVerifier = oauth2.GenerateVerifier()
	return append(opts, oauth2.S256ChallengeOption(pkceVerifier)), pkceVerifier
}

// exchangeOptions turns the verifier stored at login into the code-exchange
// options. An empty verifier means the authorization request carried no
// challenge, so the exchange must carry no code_verifier either: sending an
// empty one is a malformed request that a provider is right to refuse.
func exchangeOptions(pkceVerifier string) []oauth2.AuthCodeOption {
	if pkceVerifier == "" {
		return nil
	}
	return []oauth2.AuthCodeOption{oauth2.VerifierOption(pkceVerifier)}
}

// HandleCallback exchanges code for tokens using the PKCE verifier issued at
// login (none when authentication.disable_pkce is set), verifies
// the ID token (signature, audience, expiry, and that its nonce claim matches
// nonce), extracts identity fields per config.OAuthFields, and upserts the
// corresponding model.User.
func (s *AuthService) HandleCallback(ctx context.Context, code string, nonce string, pkceVerifier string) (*Identity, error) {
	token, err := s.oauth2Config.Exchange(ctx, code, exchangeOptions(pkceVerifier)...)
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

	// The account identifier, from whichever claim the operator named. Not
	// idToken.Subject: that is always the literal "sub", and "sub" is not
	// the stable identifier everywhere — Entra ID issues a per-application
	// one and puts the tenant-stable value in "oid". A login with no
	// identifier cannot be keyed to a user row at all, so an absent or
	// empty claim fails the login rather than inventing one.
	subjectID, ok := scalarClaim(claims, fields.Subject)
	if !ok || subjectID == "" {
		return nil, fmt.Errorf("OIDC ID token is missing the configured subject claim %q", fields.Subject)
	}

	username, ok := claims[fields.Username].(string)
	if !ok || username == "" {
		return nil, fmt.Errorf("OIDC ID token is missing the configured username claim %q", fields.Username)
	}

	// The human-readable name is optional in every direction: an
	// unconfigured claim, an absent one, or one of the wrong shape all
	// leave it empty, and nothing downstream requires it. It is display
	// only.
	displayName, _ := scalarClaim(claims, fields.Name)

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
		Subject:         subjectID,
		Username:        username,
		Email:           email,
		DisplayName:     displayName,
		Groups:          groups,
		OtherAccounts:   otherAccounts,
		ServiceAccounts: serviceAccounts,
		Extra:           extraClaims(claims, fields.Extra),
	}

	if err := s.upsertUser(ctx, identity); err != nil {
		return nil, fmt.Errorf("failed to persist user: %w", err)
	}

	// Resolved after the upsert so the audit events carry the users-row id
	// their timelines group by. A lookup failure is not worth failing a
	// login over; the event still records the identity snapshot.
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error; err != nil {
		slog.Warn("failed to resolve the users row for an audit event",
			slog.String("subject", identity.Subject), slog.Any("error", err))
	}
	// Directory enrichment, before the disabled check so a user disabled by
	// the sync is evaluated against fresh data. Never fails the login: an
	// unreachable directory logs and leaves the OIDC identity as it stands.
	s.ldap.Enrich(ctx, identity, user.ID)

	// OIDC group capture, which is useful even with LDAP disabled: it gives
	// notifications a fan-out target, just a staler one (per login rather than
	// per sync). Filtered through the same allowlist, and never an authorization
	// input — see https://mnestor.github.io/ssoossh/internals/invariants/.
	s.captureOIDCGroups(ctx, identity, user.ID)

	subject := AuditSubjectFromIdentity(identity, user.ID)

	// Check if the user has been disabled by an admin
	if err := s.checkUserDisabled(ctx, identity.Subject); err != nil {
		// A denied login vanishes entirely without this: the account is
		// disabled, so nothing else records the attempt.
		var disabled *errorresponses.UserDisabledError
		if errors.As(err, &disabled) {
			s.audit(ctx, AuditEvent{
				Action: AuditAuthLoginDenied,
				Actor:  subject,
				Target: subject,
				Detail: map[string]any{"reason": "account is disabled"},
			})
		}
		return nil, err
	}

	// The groups snapshot is the point: membership is never persisted, so
	// this is the only durable record of what access the identity carried
	// on a given day.
	s.audit(ctx, AuditEvent{Action: AuditAuthLogin, Actor: subject, Target: subject})

	return identity, nil
}

// upsertUser creates or updates the model.User row for identity, keyed by
// Subject. Group membership is deliberately not persisted here (see
// https://mnestor.github.io/ssoossh/internals/invariants/).
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
	// A nil map (no extras configured) marshals to "null"; store "{}" so
	// readers can always unmarshal the column as a map.
	extras := identity.Extra
	if extras == nil {
		extras = map[string]extraValue{}
	}
	extraFieldsJSON, err := json.Marshal(extras)
	if err != nil {
		// not covered: extraValue.MarshalJSON only encodes strings and
		// []string, so json.Marshal cannot fail on this map.
		return err
	}

	now := time.Now()
	user := model.User{
		ID:              uuid.NewString(),
		Subject:         identity.Subject,
		Username:        identity.Username,
		Email:           identity.Email,
		DisplayName:     identity.DisplayName,
		OtherAccounts:   string(otherAccountsJSON),
		ServiceAccounts: string(serviceAccountsJSON),
		ExtraFields:     string(extraFieldsJSON),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "subject"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"username", "email", "display_name", "other_accounts", "service_accounts", "extra_fields", "updated_at",
		}),
	}).Create(&user).Error
}

// scalarClaim reads key from claims as a single string, coercing the scalar
// JSON shapes the way extraClaims does: a string is itself, a bool and a
// number render as text. An unconfigured key, an absent claim, or a
// composite value (array, object) reports false.
//
// Numbers are accepted because an account identifier is not always a
// string — a provider fronting a database commonly issues a numeric user
// id — and encoding/json decodes every JSON number as float64.
func scalarClaim(claims map[string]any, key string) (string, bool) {
	if key == "" {
		return "", false
	}
	switch v := claims[key].(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	default:
		return "", false
	}
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

// extraClaims extracts the operator-configured extra fields (mapping:
// template name -> claim name, see config.OAuthFields.Extra) from claims.
// Scalars (string, bool, number) coerce to strings; an array keeps its
// string elements as a list. A missing, null, or unsupported-shape claim
// warns and stores empty — the same optional-field posture as
// stringSliceClaim — which key ID templates later render as MISSING.
// Returns nil when no extras are configured.
func extraClaims(claims map[string]any, mapping map[string]string) map[string]extraValue {
	if len(mapping) == 0 {
		return nil
	}

	extras := make(map[string]extraValue, len(mapping))
	for name, claim := range mapping {
		raw, ok := claims[claim]
		if !ok || raw == nil {
			slog.Warn("OIDC ID token is missing the configured extra claim",
				slog.String("field", name), slog.String("claim", claim))
			extras[name] = scalarExtra("")
			continue
		}

		switch v := raw.(type) {
		case string:
			extras[name] = scalarExtra(v)
		case bool:
			extras[name] = scalarExtra(strconv.FormatBool(v))
		case float64:
			// encoding/json decodes every JSON number as float64; -1
			// precision renders integral values without a decimal point.
			extras[name] = scalarExtra(strconv.FormatFloat(v, 'f', -1, 64))
		case []any:
			values := make([]string, 0, len(v))
			for _, e := range v {
				if s, ok := e.(string); ok {
					values = append(values, s)
				}
			}
			extras[name] = listExtra(values)
		default:
			slog.Warn("configured extra claim has an unsupported shape",
				slog.String("field", name), slog.String("claim", claim))
			extras[name] = scalarExtra("")
		}
	}
	return extras
}

// checkUserDisabled verifies the user identified by subject is not disabled.
// If disabled, returns a UserDisabledError that prevents session establishment.
// Fails closed: this is an authorization decision. A database error must NOT
// establish a session, because a transient blip must not admit a user an admin
// has explicitly disabled. A non-existent user row (first login) is NOT an error
// and must succeed. On query failure, returns UserStatusCheckError (503) rather
// than UserDisabledError (403), since the error is about system state, not user
// status.
func (s *AuthService) checkUserDisabled(ctx context.Context, subject string) error {
	var disabledAt sql.NullTime
	result := s.db.WithContext(ctx).
		Model(&model.User{}).
		Select("disabled_at").
		Where("subject = ?", subject).
		Scan(&disabledAt)

	// No row for this subject is not an error — first-time login. Only a
	// genuine query failure fails closed.
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Log the error for debugging, then fail closed with a service-level error
		// rather than claiming the user is disabled when we couldn't determine it
		slog.Warn("failed to check if user is disabled",
			slog.String("subject", subject), slog.Any("error", result.Error))
		return &errorresponses.UserStatusCheckError{}
	}

	if disabledAt.Valid {
		return &errorresponses.UserDisabledError{}
	}

	return nil
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

// captureOIDCGroups persists the identity's OIDC groups, filtered through
// the configured allowlist.
//
// Best-effort: a failure here must not fail a login, since these rows feed
// notification fan-out and display rather than any authorization decision.
// The write is a replace, so a membership that disappeared is actually
// gone rather than lingering.
func (s *AuthService) captureOIDCGroups(ctx context.Context, identity *Identity, userID string) {
	allowlist := groupAllowlist(s.config)
	if len(allowlist) == 0 || userID == "" {
		return
	}

	names := make([]string, 0, len(identity.Groups))
	for _, g := range identity.Groups {
		if slices.Contains(allowlist, g) {
			names = append(names, g)
		}
	}

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return replaceGroups(tx, userID, model.GroupSourceOIDC, dedupe(names), time.Now())
	}); err != nil {
		slog.Warn("failed to capture OIDC group membership",
			slog.String("subject", identity.Subject), slog.Any("error", err))
	}
}

// ClaimMapping says which configured field consumes each claim name, so an
// echo of the caller's ID token can be annotated against the configuration
// rather than printed raw. A claim dump is a curiosity; a claim dump that
// names the key each value lands in — and says which values nothing reads —
// is a config-authoring tool.
type ClaimMapping struct {
	// Subject names the claim the account identifier is read from, and is
	// the one worth checking first in an echo: everything else can be
	// wrong and be fixed, while a subject claim that varies between logins
	// forks a person's history into a new account each time.
	Subject string

	// Username, Groups, OtherAccounts, ServiceAccounts, Email and Name are
	// the claim names the reserved fields read. Empty means the field is
	// unconfigured, which for OtherAccounts and ServiceAccounts is the
	// default.
	Username        string
	Groups          string
	OtherAccounts   string
	ServiceAccounts string
	Email           string
	Name            string

	// Extra maps each configured extra field name to the claim it reads,
	// which is the half an operator is usually trying to get right.
	Extra map[string]string
}

// ClaimMapping reports the configured claim names.
func (s *AuthService) ClaimMapping() ClaimMapping {
	fields := s.config.AuthConfig.Fields

	// The email fallback is opportunistic in HandleCallback, so it is
	// reported the same way here rather than as unconfigured: the echo
	// should say which claim the value would actually come from.
	email := fields.Email
	if email == "" {
		email = "email"
	}

	extra := make(map[string]string, len(fields.Extra))
	for name, claim := range fields.Extra {
		extra[name] = claim
	}

	return ClaimMapping{
		Subject:         fields.Subject,
		Username:        fields.Username,
		Groups:          fields.Groups,
		OtherAccounts:   fields.OtherAccounts,
		ServiceAccounts: fields.ServiceAccounts,
		Email:           email,
		Name:            fields.Name,
		Extra:           extra,
	}
}

// EchoAuthorizationURL is AuthorizationURL with prompt=login.
//
// The caller already holds a session; the point of the echo is to see what
// the identity provider sends now, so reusing the provider's session would
// answer a slightly different question and would silently succeed when the
// provider's session is the stale part.
func (s *AuthService) EchoAuthorizationURL(ctx context.Context, state string) (authURL string, nonce string, pkceVerifier string, err error) {
	nonce, err = randomToken()
	if err != nil {
		// not covered: randomToken fails only if crypto/rand.Read does,
		// which crashes the process rather than returning an error.
		return "", "", "", fmt.Errorf("failed to generate OIDC nonce: %w", err)
	}

	opts, pkceVerifier := s.authCodeOptions(nonce)
	opts = append(opts, oauth2.SetAuthURLParam("prompt", "login"))

	return s.oauth2Config.AuthCodeURL(state, opts...), nonce, pkceVerifier, nil
}

// EchoCallback verifies the callback exactly as HandleCallback does and
// returns the decoded ID token claims.
//
// It establishes nothing. No session is set, no users row is written, no
// login event is recorded, and the claims are returned rather than stored:
// the server deliberately keeps only what the configuration maps, and an
// echo that quietly kept the rest would be the thing this feature exists to
// avoid.
func (s *AuthService) EchoCallback(ctx context.Context, code string, nonce string, pkceVerifier string) (map[string]any, error) {
	token, err := s.oauth2Config.Exchange(ctx, code, exchangeOptions(pkceVerifier)...)
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
	return claims, nil
}
