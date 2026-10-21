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

	// Groups is what every role and policy check reads: the OIDC groups
	// claim, narrowed by the directory when ldap.authoritative_role_groups
	// is on (see LDAPService.NarrowRoleGroups). Never widened by anything.
	Groups          []string
	OtherAccounts   []string
	ServiceAccounts []string

	// ClaimGroups is the groups claim exactly as a login read it, before
	// any narrowing. Set only by AuthService.HandleCallback, and only so
	// the session stores the claim rather than the narrowed list: each
	// request narrows afresh (IdentityService.RefreshIdentity), so a role
	// group the directory lists again counts again without another login.
	// Never an authorization input; read it through SessionGroups.
	ClaimGroups []string

	// Extra holds the operator-configured extra claim fields (see
	// config.OAuthFields.Extra), keyed by template name. Populated from ID
	// token claims at login and persisted on model.User.ExtraFields; the
	// approval path re-hydrates it from that row (the session does not
	// carry it). Consumed by key ID templates as {{.Extra.name}}.
	Extra map[string]extraValue
}

// SessionGroups is the group list a login session stores: the claim as the
// login read it when there is one, otherwise Groups (an identity built
// some other way). What a request then acts on is that list narrowed
// again, never this one.
func (i *Identity) SessionGroups() []string {
	if i.ClaimGroups != nil {
		return i.ClaimGroups
	}
	return i.Groups
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
	// and returns the decoded ID token claims (and the access token's,
	// when it is a JWT), establishing nothing: no session, no user row, no
	// login event.
	EchoCallback(ctx context.Context, code string, nonce string, pkceVerifier string) (*EchoTokens, error)

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

	// httpClient is the client discovery used (nil for the default), for
	// the UserInfo call, which go-oidc makes through the context.
	httpClient *http.Client

	// userInfoHealth pauses the UserInfo call when the endpoint keeps
	// failing, shared across instances. Nil in tests that do not exercise
	// it, which then call on every login.
	userInfoHealth *UserInfoHealthService

	// ldap enriches the identity from the directory. Nil when LDAP is
	// disabled, which is the common case and needs no branch at the call
	// site beyond the nil check inside Enrich.
	ldap *LDAPService
}

// SetLDAP wires directory enrichment. Called once at startup.
func (s *AuthService) SetLDAP(l *LDAPService) { s.ldap = l }

// SetUserInfoHealth wires the UserInfo endpoint's shared health state.
// Called once at startup.
func (s *AuthService) SetUserInfoHealth(h *UserInfoHealthService) { s.userInfoHealth = h }

// UserInfoEndpoint is the userinfo_endpoint the provider's discovery
// document names, or "" when it names none.
func (s *AuthService) UserInfoEndpoint() string { return s.provider.UserInfoEndpoint() }

// UserInfoEnabled reports authentication.userinfo.
func (s *AuthService) UserInfoEnabled() bool { return s.config.AuthConfig.UserInfo }

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
		config:     c,
		db:         db,
		httpClient: httpClient,
		provider:   provider,
		verifier:   verifier,
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
// nonce), fills in the claims it lacks from UserInfo (authentication.userinfo),
// extracts identity fields per config.OAuthFields, and upserts the
// corresponding model.User.
func (s *AuthService) HandleCallback(ctx context.Context, code string, nonce string, pkceVerifier string) (*Identity, error) {
	token, idClaims, err := s.exchangeAndVerify(ctx, code, nonce, pkceVerifier)
	if err != nil {
		return nil, err
	}

	// UserInfo fills what the ID token lacks, before any mapping.
	merged, err := s.mergeUserInfo(ctx, token, idClaims)
	if err != nil {
		return nil, err
	}

	// The mapping itself is shared with the claims echo, which reports
	// what this login would conclude without running the rest of it.
	identity, err := identityFromClaims(merged.claims, s.ClaimMapping())
	if err != nil {
		return nil, err
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

	// After the capture, which stores the claim as it came, and after
	// enrichment, which has just rewritten the directory rows this reads.
	// From here on identity.Groups is what the session acts on.
	narrowed := s.narrowRoleGroupsAtLogin(ctx, identity, user.ID)

	// After enrichment and narrowing, so the default reads the roles this
	// session will actually hold. Only fills an unset level; see
	// DefaultDetailLevel.
	s.fillDetailLevelsAtLogin(ctx, identity)

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
	// on a given day. The snapshot is the narrowed list, the access the
	// session holds; a role group the directory took away is named in the
	// detail, so the claim it came from is not lost.
	login := AuditEvent{Action: AuditAuthLogin, Actor: subject, Target: subject}
	if len(narrowed) > 0 {
		login.Detail = map[string]any{"role_groups_removed_by_directory": narrowed}
	}
	s.audit(ctx, login)

	return identity, nil
}

// narrowRoleGroupsAtLogin applies ldap.authoritative_role_groups to a
// fresh login: a role group the claim carries but the latest directory
// read for this user no longer lists is removed from identity.Groups, the
// same rule every later request applies. The claim itself is kept in
// identity.ClaimGroups for the session. Returns the groups removed.
//
// A database error narrows rather than fails the login (see
// LDAPService.NarrowRoleGroups): the person signs in without the roles
// that could not be checked, and the next request checks again.
func (s *AuthService) narrowRoleGroupsAtLogin(ctx context.Context, identity *Identity, userID string) []string {
	identity.ClaimGroups = identity.Groups
	groups, err := s.ldap.NarrowRoleGroups(ctx, userID, identity.Groups)
	if err != nil {
		slog.WarnContext(ctx, "could not check the role groups against the directory, so this login holds none of them",
			slog.String("subject", identity.Subject), slog.Any("error", err))
	}
	removed := removedGroups(identity.Groups, groups)
	identity.Groups = groups
	return removed
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
		_, err := replaceGroups(tx, userID, model.GroupSourceOIDC, dedupe(names), time.Now())
		return err
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

// EchoTokens is what a claims echo hands back: the verified ID token's
// claims, and the access token's, decoded but never verified.
type EchoTokens struct {
	// Claims is what a login maps: the decoded ID token, verified exactly
	// as a login verifies it, with the claims UserInfo filled in.
	Claims map[string]any

	// Sources says where each claim came from: ClaimSourceIDToken or
	// ClaimSourceUserInfo.
	Sources map[string]string

	// UserInfo is what happened to the UserInfo call.
	UserInfo UserInfoStatus

	// AccessTokenClaims is the access token's payload when the provider
	// issues a JWT one, and nil otherwise. Unverified, and never read by a
	// login: it is here because "groups is in the access token, not the ID
	// token" is a common provider mistake, and seeing it is how an
	// operator finds out. The token itself never leaves this function.
	AccessTokenClaims map[string]any
}

// EchoCallback verifies the callback exactly as HandleCallback does and
// returns the decoded ID token claims, with the access token's when it is
// a JWT.
//
// It establishes nothing. No session is set, no users row is written, no
// login event is recorded, and the claims are returned rather than stored:
// the server deliberately keeps only what the configuration maps, and an
// echo that quietly kept the rest would be the thing this feature exists to
// avoid.
func (s *AuthService) EchoCallback(ctx context.Context, code string, nonce string, pkceVerifier string) (*EchoTokens, error) {
	token, idClaims, err := s.exchangeAndVerify(ctx, code, nonce, pkceVerifier)
	if err != nil {
		return nil, err
	}
	merged, err := s.mergeUserInfo(ctx, token, idClaims)
	if err != nil {
		return nil, err
	}
	return &EchoTokens{
		Claims:            merged.claims,
		Sources:           merged.sources,
		UserInfo:          merged.status,
		AccessTokenClaims: unverifiedJWTClaims(token.AccessToken),
	}, nil
}

// exchangeAndVerify is the half of a callback a login and an echo share:
// exchange code for tokens using the PKCE verifier issued at the start
// (none when authentication.disable_pkce is set), then verify the ID token
// (signature, audience, expiry, and that its nonce claim matches nonce)
// and decode its claims.
func (s *AuthService) exchangeAndVerify(ctx context.Context, code, nonce, pkceVerifier string) (*oauth2.Token, map[string]any, error) {
	token, err := s.oauth2Config.Exchange(ctx, code, exchangeOptions(pkceVerifier)...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to exchange OIDC authorization code: %w", err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, nil, errors.New("OIDC token response is missing id_token")
	}

	idToken, err := s.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to verify OIDC ID token: %w", err)
	}

	if idToken.Nonce != nonce {
		return nil, nil, errors.New("OIDC ID token nonce does not match the one issued at login")
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		// not covered: Verify above already parsed this same payload as
		// JSON to validate the standard claims, so re-unmarshaling it
		// here cannot fail.
		return nil, nil, fmt.Errorf("failed to parse OIDC ID token claims: %w", err)
	}
	return token, claims, nil
}

// unverifiedJWTClaims decodes the payload of a JWT without checking its
// signature, or returns nil for anything that is not one (an opaque access
// token, most often). Display only: nothing may act on what it returns.
func unverifiedJWTClaims(raw string) map[string]any {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}
