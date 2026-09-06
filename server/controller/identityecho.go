package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// echoResultPath is where the callback sends the browser with the echoed
// claims in the fragment.
const echoResultPath = "/admin/identity/echo"

// NewIdentityEchoController registers the claims echo's start endpoint. The
// other half of the flow is the existing OIDC callback, which branches on
// the session flag this sets.
//
// Admin-only. It shows the caller their own claims and nobody else's, but it
// drives a fresh authorization round trip against the identity provider, and
// the annotated result is a configuration tool rather than a user-facing
// one.
func NewIdentityEchoController(
	group *gin.RouterGroup,
	authService service.AuthProvider,
	sessionAuthMiddleware gin.HandlerFunc,
	adminAuthMiddleware gin.HandlerFunc,
	csrfMiddleware gin.HandlerFunc,
) {
	c := &identityEchoController{authService: authService}

	adminGroup := group.Group("/admin/identity", sessionAuthMiddleware, adminAuthMiddleware, csrfMiddleware)
	adminGroup.POST("/echo/start", c.startHandler)
}

// identityEchoController starts a claims echo.
type identityEchoController struct {
	authService service.AuthProvider
}

// startHandler handles POST /api/admin/identity/echo/start.
//
// @Summary     Start a claims echo (admin-only)
// @Description Returns the authorization URL for a fresh OIDC round trip
// @Description carrying prompt=login. The callback renders the decoded ID
// @Description token instead of establishing a session, and the caller's
// @Description existing session is left exactly as it was.
// @Description
// @Description The echo re-authenticates rather than remembering: the server
// @Description keeps only the claims the configuration maps, so there is no
// @Description stored copy of the rest to show. Nothing about the result is
// @Description written down — it reaches the page in the redirect fragment,
// @Description which never reaches the server at all.
// @Tags        admin
// @Produce     json
// @Success     200 {object} webtypes.IdentityEchoStartResponse "Where to send the browser"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Security    sessionCookie
// @Router      /api/admin/identity/echo/start [post]
func (c *identityEchoController) startHandler(g *gin.Context) {
	state, err := randomState()
	if err != nil {
		// not covered: randomState fails only if crypto/rand.Read does,
		// which crashes the process rather than returning an error.
		handleError(g, err)
		return
	}

	authURL, nonce, verifier, err := c.authService.EchoAuthorizationURL(g.Request.Context(), state)
	if err != nil {
		handleError(g, err)
		return
	}

	if err := middleware.SetOIDCEchoState(g, state, nonce, verifier); err != nil {
		handleError(g, err)
		return
	}

	respondData(g, webtypes.IdentityEchoStartResponse{AuthorizationURL: authURL})
}

// echoRedirectURL builds the redirect that hands claims to the echo page.
//
// The payload rides in the fragment because a fragment is never sent to a
// server: not to this one, not to a proxy in front of it, and not into any
// access log. That is what makes "never saved" true rather than merely
// intended — there is no store to forget to clear, and the page drops the
// fragment from history as soon as it has read it.
func echoRedirectURL(claims map[string]any, mapping service.ClaimMapping, now time.Time) (string, error) {
	payload := webtypes.IdentityEchoPayload{
		Claims:      claims,
		Mapping:     newClaimMappingResponse(mapping),
		Suggestions: claimSuggestions(claims, mapping),
		IssuedAt:    now,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode the echoed claims: %w", err)
	}

	// Base64url rather than percent-encoded JSON: the fragment carries
	// arbitrary claim values, and one alphabet that needs no escaping is
	// less to get wrong than two layers of escaping that both do.
	return echoResultPath + "#" + url.PathEscape(base64.RawURLEncoding.EncodeToString(encoded)), nil
}

// newClaimMappingResponse converts the configured mapping to its wire shape.
func newClaimMappingResponse(mapping service.ClaimMapping) webtypes.ClaimMappingResponse {
	return webtypes.ClaimMappingResponse{
		Subject:         mapping.Subject,
		Name:            mapping.Name,
		Username:        mapping.Username,
		Groups:          mapping.Groups,
		OtherAccounts:   mapping.OtherAccounts,
		ServiceAccounts: mapping.ServiceAccounts,
		Email:           mapping.Email,
		Extra:           mapping.Extra,
	}
}

// claimSuggestions turns the claims nothing reads into the config lines that
// would capture them.
//
// This is what makes the echo a tool rather than a curiosity. A claim dump
// answers "what does my IdP send"; a claim dump that names the key each
// value would land under answers "what do I write in the config file".
func claimSuggestions(claims map[string]any, mapping service.ClaimMapping) []webtypes.ClaimSuggestion {
	consumed := map[string]bool{}
	for _, name := range []string{
		mapping.Subject, mapping.Username, mapping.Name, mapping.Groups,
		mapping.OtherAccounts, mapping.ServiceAccounts, mapping.Email,
	} {
		if name != "" {
			consumed[name] = true
		}
	}
	for _, claim := range mapping.Extra {
		consumed[claim] = true
	}

	names := make([]string, 0, len(claims))
	for name := range claims {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []webtypes.ClaimSuggestion
	for _, name := range names {
		if consumed[name] || isProtocolClaim(name) {
			continue
		}
		out = append(out, webtypes.ClaimSuggestion{
			Claim:  name,
			Reason: claimSuggestionReason(claims[name]),
			YAML: fmt.Sprintf("authentication:\n  fields:\n    extra:\n      %s: %s",
				suggestedExtraName(name), yamlClaimName(name)),
		})
	}
	return out
}

// claimSuggestionReason says what the value looks like, since the shape is
// what decides whether it can gate a policy condition or only decorate a key
// ID.
func claimSuggestionReason(value any) string {
	switch v := value.(type) {
	case float64:
		return "numeric, so a policy condition could compare against it"
	case bool:
		return "boolean; captured as its string form"
	case []any:
		return fmt.Sprintf("a list of %d value(s); captured as a list", len(v))
	case string:
		return "unmapped: nothing in the configuration reads this claim"
	default:
		return "unmapped, and not a scalar or list; captured as its string form"
	}
}

// protocolClaims are the JWT and OIDC mechanics rather than identity, so
// suggesting a mapping for them would be noise.
var protocolClaims = []string{
	"iss", "aud", "exp", "iat", "nbf", "jti", "nonce", "azp", "at_hash",
	"c_hash", "auth_time", "sid", "typ", "sub",
}

// isProtocolClaim reports whether name is protocol mechanics.
func isProtocolClaim(name string) bool {
	for _, claim := range protocolClaims {
		if claim == name {
			return true
		}
	}
	return false
}

// suggestedExtraName turns a claim name into an extra field name a template
// can reach with dotted syntax. A namespaced claim keeps only its last
// segment, since that is what an operator would name it anyway.
func suggestedExtraName(claim string) string {
	name := claim
	if i := strings.LastIndexAny(name, "/:#"); i >= 0 && i+1 < len(name) {
		name = name[i+1:]
	}
	var out strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r + ('a' - 'A'))
		default:
			out.WriteByte('_')
		}
	}
	return out.String()
}

// yamlClaimName quotes a claim name that YAML would otherwise misread — a
// namespaced claim URL, most often.
func yamlClaimName(claim string) string {
	if strings.ContainsAny(claim, ":#{}[],&*?|<>=!%@`\"' ") {
		return fmt.Sprintf("%q", claim)
	}
	return claim
}
