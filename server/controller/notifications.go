package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewNotificationController registers the caller's own notification
// preferences on group, behind session auth. The update also carries CSRF,
// like every other state-changing browser route.
//
// The routes sit under /users/me for the same reason /users/me itself does:
// these are the caller's own settings, and there is deliberately no route
// to read or change anyone else's.
//
// levels carries the email detail level, which is read and saved with the
// per-kind answers because the page shows them in one section.
func NewNotificationController(group *gin.RouterGroup, prefs service.NotificationPreferenceProvider, levels service.DetailLevelProvider,
	sessionAuthMiddleware, csrfMiddleware gin.HandlerFunc) {
	n := &notificationController{prefs: prefs, levels: levels}

	group.GET("/users/me/notifications", sessionAuthMiddleware, n.preferencesHandler)
	group.PUT("/users/me/notifications", sessionAuthMiddleware, csrfMiddleware, n.updatePreferencesHandler)
}

// notificationController serves the notification preferences page.
type notificationController struct {
	prefs  service.NotificationPreferenceProvider
	levels service.DetailLevelProvider
}

// preferencesHandler handles GET /api/users/me/notifications: every
// notification the server can send, with this user's answer for each.
//
// The kind list comes from the server rather than the frontend so that
// adding a notification type is a server-side change — the page renders
// whatever it is given.
//
// @Summary     The caller's own notification preferences
// @Description Every notification kind this server knows how to send, with the
// @Description caller's answer for each. Kinds the caller has never answered report
// @Description the kind's own default. Also reports whether the server is configured
// @Description to send mail at all, the address it would send to, and how much
// @Description each message carries (the email detail level).
// @Tags        web
// @Produce     json
// @Success     200 {object} openapidoc.NotificationPreferencesEnvelope "The caller's notification preferences"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "The session has no user record"
// @Security    sessionCookie
// @Router      /api/users/me/notifications [get]
func (n *notificationController) preferencesHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	n.respondPreferences(g, identity)
}

// updatePreferencesHandler handles PUT /api/users/me/notifications.
//
// Only the kinds named in the body are changed. A client that knows about
// fewer kinds than the server — an older tab, or a page loaded before an
// upgrade — therefore cannot reset preferences for kinds it has never heard
// of by saving the form it has.
//
// The fresh preferences are returned so the page renders what was actually
// stored rather than what it submitted.
//
// @Summary     Update the caller's own notification preferences
// @Description Sets the caller's answer for each notification kind named in the body,
// @Description leaving every other kind unchanged. An unrecognized kind is rejected
// @Description rather than ignored, and nothing is stored when one is present.
// @Description detail_level, when present, sets the email detail level in the same save.
// @Tags        web
// @Accept      json
// @Produce     json
// @Param       request body webtypes.UpdateNotificationPreferencesBody true "The kinds to change"
// @Success     200 {object} openapidoc.NotificationPreferencesEnvelope "The preferences after saving"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Malformed body, an unrecognized notification kind, or an unknown detail level"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "The session has no user record"
// @Security    sessionCookie
// @Router      /api/users/me/notifications [put]
func (n *notificationController) updatePreferencesHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	var body webtypes.UpdateNotificationPreferencesBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "notification preferences body was malformed"})
		return
	}

	// The level is checked before anything is written, so a bad one leaves
	// the kinds untouched too: a save either applies whole or not at all as
	// far as the caller's input can decide.
	if body.DetailLevel != nil && !body.DetailLevel.Valid() {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "unknown detail level " + strconv.Quote(string(*body.DetailLevel))})
		return
	}

	updates := make(map[notify.Kind]bool, len(body.Kinds))
	for kind, enabled := range body.Kinds {
		updates[notify.Kind(kind)] = enabled
	}

	ctx := g.Request.Context()
	if err := n.prefs.SetPreferencesForIdentity(ctx, identity, updates); err != nil {
		handleError(g, err)
		return
	}
	if body.DetailLevel != nil {
		if err := n.levels.SetEmail(ctx, identity, *body.DetailLevel); err != nil {
			handleError(g, err)
			return
		}
	}

	n.respondPreferences(g, identity)
}

// respondPreferences writes the caller's stored preferences and email
// detail level, the shape both routes return.
func (n *notificationController) respondPreferences(g *gin.Context, identity *service.Identity) {
	ctx := g.Request.Context()
	settings, err := n.prefs.PreferencesForIdentity(ctx, identity)
	if err != nil {
		handleError(g, err)
		return
	}
	levels, err := n.levels.Levels(ctx, identity)
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, newNotificationPreferencesResponse(settings, levels.Email))
}

// newNotificationPreferencesResponse converts the service's view to the
// wire shape.
func newNotificationPreferencesResponse(settings service.NotificationSettings, level model.DetailLevel) webtypes.NotificationPreferencesResponse {
	kinds := make([]webtypes.NotificationKindResponse, 0, len(settings.Kinds))
	for _, pref := range settings.Kinds {
		kinds = append(kinds, webtypes.NotificationKindResponse{
			Kind:        string(pref.Kind),
			Group:       string(pref.Group),
			Title:       pref.Title,
			Description: pref.Description,
			Enabled:     pref.Enabled,
		})
	}

	// Every registered group, in order, even one this user's kinds leave
	// empty: the page heads each group from this list and lists the kinds
	// that name it, so the order is the registry's and not the browser's.
	registered := notify.Groups()
	groups := make([]webtypes.NotificationGroupResponse, 0, len(registered))
	for _, g := range registered {
		groups = append(groups, webtypes.NotificationGroupResponse{
			Group:       string(g.Group),
			Title:       g.Title,
			Description: g.Description,
		})
	}

	return webtypes.NotificationPreferencesResponse{
		MailEnabled: settings.MailEnabled,
		Address:     settings.Address,
		Groups:      groups,
		Kinds:       kinds,
		DetailLevel: level,
	}
}
