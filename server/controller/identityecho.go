package controller

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// echoResultPath is where the callback sends the browser with the echoed
// claims in the fragment, for the admin echo.
const echoResultPath = "/admin/identity/echo"

// bootstrapEchoResultPath is the same for a bootstrap echo: the public
// page, since whoever opened the link may hold no role, or no account yet.
const bootstrapEchoResultPath = "/identity/echo"

// NewIdentityEchoController registers the claims echo's start endpoint. The
// other half of the flow is the existing OIDC callback, which branches on
// the session flag this sets.
//
// Admin-only. It shows the caller their own claims and nobody else's, but it
// drives a fresh authorization round trip against the identity provider, and
// the annotated result is a configuration tool rather than a user-facing
// one.
//
// The bootstrap echo's redeem endpoint is the one route here that needs no
// session: it trades a one-time code minted on the server host (`ssoosshd
// identity echo`) for the same authorization URL. It grants nothing either:
// the echo it starts shows the person their own claims and establishes no
// session. CSRF-guarded like every state-changing route, and behind the
// global rate limiter.
func NewIdentityEchoController(
	group *gin.RouterGroup,
	authService service.AuthProvider,
	echoCodes service.IdentityEchoCodes,
	sessionAuthMiddleware gin.HandlerFunc,
	adminAuthMiddleware gin.HandlerFunc,
	csrfMiddleware gin.HandlerFunc,
) {
	c := &identityEchoController{authService: authService, echoCodes: echoCodes}

	adminGroup := group.Group("/admin/identity", sessionAuthMiddleware, adminAuthMiddleware, csrfMiddleware)
	adminGroup.POST("/echo/start", c.startHandler)

	group.POST("/identity/echo/redeem", csrfMiddleware, c.redeemHandler)
}

// identityEchoController starts a claims echo.
type identityEchoController struct {
	authService service.AuthProvider
	echoCodes   service.IdentityEchoCodes
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

// redeemHandler handles POST /api/identity/echo/redeem.
//
// @Summary     Redeem a bootstrap echo link
// @Description Consumes the one-time code a `ssoosshd identity echo` link
// @Description carries in its fragment and returns the authorization URL
// @Description for a claims echo, as the admin start does. No session or
// @Description role is needed, and none is granted: the echo shows whoever
// @Description signs in their own claims and establishes nothing.
// @Description
// @Description A code works once. A wrong, used or expired code all get the
// @Description same 404, so the endpoint tells a guesser nothing.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body webtypes.IdentityEchoRedeemRequest true "The code from the link"
// @Success     200 {object} webtypes.IdentityEchoStartResponse "Where to send the browser"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Malformed body"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Cross-origin call"
// @Failure     404 {object} openapidoc.ErrorEnvelope "The code is wrong, used or expired"
// @Router      /api/identity/echo/redeem [post]
func (c *identityEchoController) redeemHandler(g *gin.Context) {
	var body webtypes.IdentityEchoRedeemRequest
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "request body was malformed or did not match the expected schema"})
		return
	}

	// The address the server observed (http.trusted_proxies honoured),
	// never one the caller states.
	codeID, err := c.echoCodes.Redeem(g.Request.Context(), body.Code, g.ClientIP())
	if err != nil {
		handleError(g, err)
		return
	}

	state, err := randomState()
	if err != nil {
		// not covered: randomState fails only if crypto/rand.Read does,
		// which crashes the process rather than returning an error.
		handleError(g, err)
		return
	}
	authURL, nonce, verifier, err := c.authService.EchoAuthorizationURL(g.Request.Context(), state)
	if err != nil {
		c.echoCodes.RecordOutcome(g.Request.Context(), codeID, err.Error())
		handleError(g, err)
		return
	}
	if err := middleware.SetOIDCBootstrapEchoState(g, state, nonce, verifier, codeID); err != nil {
		c.echoCodes.RecordOutcome(g.Request.Context(), codeID, "the server could not save the sign-in state: "+err.Error())
		handleError(g, err)
		return
	}

	respondData(g, webtypes.IdentityEchoStartResponse{AuthorizationURL: authURL})
}

// echoRedirectURL builds the redirect that hands an echo result to the
// page at resultPath.
//
// The payload rides in the fragment because a fragment is never sent to a
// server: not to this one, not to a proxy in front of it, and not into any
// access log. That is what makes "never saved" true rather than merely
// intended: there is no store to forget to clear, and the page drops the
// fragment from history as soon as it has read it.
func echoRedirectURL(resultPath string, report *service.EchoReport, now time.Time) (string, error) {
	return fragmentURL(resultPath, newIdentityEchoPayload(report, now))
}

// echoFailureURL builds the redirect for an echo that produced no claims:
// ?error= for the kind of failure, as before, and the error itself in the
// fragment, so the page can say what the provider refused instead of a
// generic failure, and no server or log sees the message on the way.
func echoFailureURL(resultPath string, reason signInFailure, message string) string {
	query := url.Values{"error": {string(reason)}}
	target := resultPath + "?" + query.Encode()
	if message == "" {
		return target
	}
	withFragment, err := fragmentURL(target, webtypes.IdentityEchoFailure{Message: message})
	if err != nil {
		// not covered: a struct holding one string always encodes.
		return target
	}
	return withFragment
}

// fragmentURL appends payload to target as a base64url JSON fragment.
// Base64url rather than percent-encoded JSON: the fragment carries
// arbitrary claim values, and one alphabet that needs no escaping is less
// to get wrong than two layers of escaping that both do.
func fragmentURL(target string, payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode the echo result: %w", err)
	}
	return target + "#" + url.PathEscape(base64.RawURLEncoding.EncodeToString(encoded)), nil
}

// newIdentityEchoPayload converts a report to its wire shape.
func newIdentityEchoPayload(report *service.EchoReport, now time.Time) webtypes.IdentityEchoPayload {
	payload := webtypes.IdentityEchoPayload{
		Claims:               report.Claims,
		ClaimSources:         report.ClaimSources,
		UserInfo:             string(report.UserInfo),
		Mapping:              newClaimMappingResponse(report.Mapping),
		Fields:               make([]webtypes.ClaimFieldResolution, 0, len(report.Fields)),
		SignIn:               webtypes.IdentityEchoSignIn{Allowed: report.SignIn.Allowed, Reason: report.SignIn.Reason},
		AdminGroupCandidates: report.AdminGroupCandidates,
		ProtocolClaims:       report.ProtocolClaims,
		IssuedAt:             now,
	}
	for _, f := range report.Fields {
		payload.Fields = append(payload.Fields, webtypes.ClaimFieldResolution{
			Field: f.Field, Claim: f.Claim, Required: f.Required, Status: string(f.Status),
			List: f.List, Value: f.Value, Values: f.Values,
		})
	}
	for _, sgg := range report.Suggestions {
		payload.Suggestions = append(payload.Suggestions, webtypes.ClaimSuggestion{
			Field: sgg.Field, Claim: sgg.Claim, Reason: sgg.Reason, YAML: sgg.YAML,
		})
	}
	for _, u := range report.NotMappable {
		payload.NotMappable = append(payload.NotMappable, webtypes.UnmappableClaim{
			Claim: u.Claim, Source: u.Source, Value: u.Value, Reason: u.Reason,
		})
	}
	return payload
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
