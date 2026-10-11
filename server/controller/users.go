package controller

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewUserController registers the current-user route on group, behind
// sessionAuthMiddleware. db is used to hydrate extra fields from the users
// table; the session remains authoritative for subject/username/email/groups/accounts.
//
// Auditor-scoped endpoints to list and read other users (for the admin
// directory) live in the admin controller, not here: those require explicit
// auditor authorization, while this endpoint is scoped to each user's own
// identity.
//
// levels serves the web UI detail level on /users/me and its update on
// /users/me/display, which carries CSRF like every other state-changing
// browser route.
func NewUserController(group *gin.RouterGroup, c *config.Config, sessionAuthMiddleware gin.HandlerFunc, db any,
	levels service.DetailLevelProvider, csrfMiddleware gin.HandlerFunc) {
	uc := &userController{config: c, db: db, levels: levels}

	group.GET("/users/me", sessionAuthMiddleware, uc.currentUserHandler)
	group.PUT("/users/me/display", sessionAuthMiddleware, csrfMiddleware, uc.updateDisplayHandler)
}

// userController handles the user-facing identity routes. It holds config
// to derive the session's roles, db to hydrate extra fields, and levels for
// the stored detail level.
type userController struct {
	config *config.Config
	db     any
	levels service.DetailLevelProvider
}

// currentUserHandler returns the caller's own identity, so the UI can show
// who it is acting as without a second round trip to the IdP.
//
// Identity (subject, username, email, groups, accounts) is sourced from the
// session, which is what actually authorizes the request — reporting anything
// else would let the UI display an identity the server would not act on.
//
// Extra fields are hydrated from the users table keyed by session subject,
// since they are stored attributes that do not change within a session.
// Malformed or missing extra fields degrade to empty rather than failing
// the page, so an operator debugging a key ID template can see that a claim
// did not arrive.
//
// @Summary     The caller's own identity
// @Description Sourced primarily from the session, with extra fields hydrated from the users table.
// @Tags        web
// @Produce     json
// @Success     200 {object} openapidoc.CurrentUserEnvelope "The session identity"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Security    sessionCookie
// @Router      /api/users/me [get]
func (uc *userController) currentUserHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	response := newCurrentUserResponse(identity, uc.config, uc.db, identity.Subject)
	response.DetailLevel = uc.webDetailLevel(g, identity)
	respondData(g, response)
}

// webDetailLevel reads the caller's stored web UI detail level. A failed
// read falls back to the level their roles would start them on rather than
// failing /users/me: every page loads it, and a display preference is not
// worth a broken UI.
func (uc *userController) webDetailLevel(g *gin.Context, identity *service.Identity) model.DetailLevel {
	if uc.levels != nil {
		levels, err := uc.levels.Levels(g.Request.Context(), identity)
		if err == nil {
			return levels.Web
		}
		slog.WarnContext(g.Request.Context(), "failed to read the web detail level; using the role default",
			slog.Any("error", err))
	}
	return service.DefaultDetailLevel(&uc.config.Admin, identity.Groups)
}

// updateDisplayHandler handles PUT /api/users/me/display: the caller sets
// their own web UI detail level. The stored value is returned so the page
// shows what was saved rather than what it sent.
//
// @Summary     Update the caller's own web UI detail level
// @Description Sets how much of the approval and information pages the caller sees:
// @Description "simplified" or "advanced". Stored on the server, so it follows the
// @Description caller between browsers. The email detail level is separate (see
// @Description /api/users/me/notifications).
// @Tags        web
// @Accept      json
// @Produce     json
// @Param       request body webtypes.UpdateDisplayPreferencesBody true "The new level"
// @Success     200 {object} openapidoc.DisplayPreferencesEnvelope "The level after saving"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Malformed body or an unknown level"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "The session has no user record"
// @Security    sessionCookie
// @Router      /api/users/me/display [put]
func (uc *userController) updateDisplayHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	var body webtypes.UpdateDisplayPreferencesBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "display preferences body was malformed"})
		return
	}

	ctx := g.Request.Context()
	if err := uc.levels.SetWeb(ctx, identity, body.DetailLevel); err != nil {
		handleError(g, err)
		return
	}
	levels, err := uc.levels.Levels(ctx, identity)
	if err != nil {
		// not covered: the same row was written moments ago.
		handleError(g, err)
		return
	}

	respondData(g, webtypes.DisplayPreferencesResponse{DetailLevel: levels.Web})
}
