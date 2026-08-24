package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// NewUserController registers the current-user route on group, behind
// sessionAuthMiddleware. db is used to hydrate extra fields from the users
// table; the session remains authoritative for subject/username/email/groups/accounts.
//
// There is deliberately no route to list or read *other* users. Nothing in
// the UI needs one yet, and an endpoint that enumerates identities is worth
// adding on demand with an authorization rule rather than by default.
func NewUserController(group *gin.RouterGroup, c *config.Config, sessionAuthMiddleware gin.HandlerFunc, db any) {
	uc := &userController{config: c, db: db}

	group.GET("/users/me", sessionAuthMiddleware, uc.currentUserHandler)
}

// userController handles the user-facing identity routes. It holds config
// only to derive the session's roles (auditor), and db to hydrate extra fields.
type userController struct {
	config *config.Config
	db     any
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

	respondData(g, newCurrentUserResponse(identity, uc.config, uc.db, identity.Subject))
}
