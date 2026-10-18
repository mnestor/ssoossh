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
//
// posix serves the caller's own POSIX account on /users/me and the shell
// self-service on /users/me/shell. Nil when NSS is disabled: /users/me then
// reports no account and the shell route answers nss_disabled.
func NewUserController(group *gin.RouterGroup, c *config.Config, sessionAuthMiddleware gin.HandlerFunc, db any,
	levels service.DetailLevelProvider, csrfMiddleware gin.HandlerFunc, posix service.PosixAccountProvider) {
	uc := &userController{config: c, db: db, levels: levels, posix: posix}

	group.GET("/users/me", sessionAuthMiddleware, uc.currentUserHandler)
	group.PUT("/users/me/display", sessionAuthMiddleware, csrfMiddleware, uc.updateDisplayHandler)
	group.PUT("/users/me/shell", uc.nssEnabled, sessionAuthMiddleware, csrfMiddleware, uc.updateShellHandler)
}

// userController handles the user-facing identity routes. It holds config
// to derive the session's roles, db to hydrate extra fields, levels for
// the stored detail level, and posix for the caller's POSIX account.
type userController struct {
	config *config.Config
	db     any
	levels service.DetailLevelProvider
	posix  service.PosixAccountProvider
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
	response.Posix = uc.posixAccount(g, identity)
	respondData(g, response)
}

// posixAccount reads the caller's own POSIX account, or nil when NSS is off
// or none is linked. A failed read degrades to nil rather than failing
// /users/me, which every page loads.
func (uc *userController) posixAccount(g *gin.Context, identity *service.Identity) *webtypes.PosixAccountResponse {
	if uc.posix == nil || !uc.config.NSS.Enabled {
		return nil
	}
	account, err := uc.posix.AccountForIdentity(g.Request.Context(), identity)
	if err != nil {
		slog.WarnContext(g.Request.Context(), "failed to read the caller's POSIX account; reporting none",
			slog.Any("error", err))
		return nil
	}
	return newPosixAccountResponse(account)
}

// newPosixAccountResponse converts an account to its wire shape, nil for
// none.
func newPosixAccountResponse(account *service.PosixAccount) *webtypes.PosixAccountResponse {
	if account == nil {
		return nil
	}
	return &webtypes.PosixAccountResponse{UID: account.UID, Name: account.Name, ShellPref: account.ShellPref}
}

// nssEnabled answers nss_disabled for the shell route while NSS is off.
func (uc *userController) nssEnabled(g *gin.Context) {
	if uc.posix == nil || !uc.config.NSS.Enabled {
		abortNSSDisabled(g)
		return
	}
	g.Next()
}

// updateShellHandler handles PUT /api/users/me/shell: the caller chooses
// the shell their own POSIX account is served with, by name, or clears it.
//
// @Summary     Choose the caller's own login shell
// @Description Sets shell_pref on the caller's POSIX account to one of nss.allowed_shells (see
// @Description /api/nss/shells), or clears it with null. Hosts resolve the name to their own path. A name
// @Description outside the list is a 400 whose message starts with shell_not_allowed. Audited.
// @Tags        web
// @Accept      json
// @Produce     json
// @Param       request body webtypes.UpdateShellBody true "The shell name, or null"
// @Success     200 {object} openapidoc.PosixAccountEnvelope "The account after saving"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Malformed body, or shell_not_allowed"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "The session has no user record"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No POSIX account is linked to the caller, or nss_disabled"
// @Security    sessionCookie
// @Router      /api/users/me/shell [put]
func (uc *userController) updateShellHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}
	var body webtypes.UpdateShellBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "shell body was malformed"})
		return
	}
	account, err := uc.posix.SetShellPref(g.Request.Context(), identity, body.ShellPref, g.ClientIP())
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, newPosixAccountResponse(account))
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
