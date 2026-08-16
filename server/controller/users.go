package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
)

// NewUserController registers the current-user route on group, behind
// sessionAuthMiddleware.
//
// There is deliberately no route to list or read *other* users. Nothing in
// the UI needs one yet, and an endpoint that enumerates identities is worth
// adding on demand with an authorization rule rather than by default.
func NewUserController(group *gin.RouterGroup, sessionAuthMiddleware gin.HandlerFunc) {
	uc := &userController{}

	group.GET("/users/me", sessionAuthMiddleware, uc.currentUserHandler)
}

// userController handles the user-facing identity routes. It holds no
// service: everything it returns already lives on the session.
type userController struct{}

// currentUserHandler returns the caller's own identity, so the UI can show
// who it is acting as without a second round trip to the IdP.
//
// Sourced from the session rather than the users table on purpose: the
// session is what actually authorizes the request, so reporting anything
// else would let the UI display an identity the server would not act on.
//
// @Summary     The caller's own identity
// @Description Sourced from the session, not the users table — the session is what
// @Description authorizes requests, so reporting anything else would let the UI display
// @Description an identity the server would not act on.
// @Description
// @Description There is deliberately no endpoint to read other users.
// @Tags        web
// @Produce     json
// @Success     200 {object} openapidoc.CurrentUserEnvelope "The session identity"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Security    sessionCookie
// @Router      /api/users/me [get]
func (uc *userController) currentUserHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		_ = g.Error(&middleware.UnauthorizedError{}) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	respondData(g, newCurrentUserResponse(identity))
}
