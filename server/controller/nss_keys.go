package controller

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewNSSController registers the session-authenticated NSS routes under
// /nss on group: the allowed shells and the fleet key administration. keys
// and lookups are nil when NSS is disabled, and every route then answers
// 404 nss_disabled, before the session is looked at.
//
// The routes are not under /admin because a key's own group manages it
// without any admin role (README R16). Who may do what is decided in
// service.NSSKeyService, not by middleware here, because it depends on the
// key as well as the caller.
func NewNSSController(group *gin.RouterGroup, keys *service.NSSKeyService, lookups *service.NSSLookupService,
	sessionAuthMiddleware gin.HandlerFunc, csrfMiddleware gin.HandlerFunc) {
	k := &nssKeyController{keys: keys, lookups: lookups}

	// One group: the CSRF middleware exempts safe methods, so the reads
	// share it with the mutations.
	nss := group.Group("/nss", k.enabled, sessionAuthMiddleware, csrfMiddleware)
	nss.GET("/shells", k.shellsHandler)
	nss.GET("/keys", k.listHandler)
	nss.POST("/keys", k.createHandler)
	nss.GET("/keys/:id", k.getHandler)
	nss.PATCH("/keys/:id", k.updateHandler)
	nss.POST("/keys/:id/rotate", k.rotateHandler)
	nss.DELETE("/keys/:id/previous", k.clearPreviousHandler)
	nss.POST("/keys/:id/revoke", k.revokeHandler)
	nss.GET("/keys/:id/usage", k.usageHandler)
	nss.GET("/keys/:id/rotation-report", k.rotationReportHandler)
}

// nssKeyController serves the session NSS routes.
type nssKeyController struct {
	keys    *service.NSSKeyService
	lookups *service.NSSLookupService
}

// enabled answers nss_disabled for every route while NSS is off.
func (k *nssKeyController) enabled(g *gin.Context) {
	if k.keys == nil || k.lookups == nil {
		abortNSSDisabled(g)
		return
	}
	g.Next()
}

// abortNSSDisabled stops the chain with 404 nss_disabled.
func abortNSSDisabled(g *gin.Context) {
	handleError(g, errorresponses.NSSDisabled())
	g.Abort()
}

// identity is the session identity, or false with the 401 registered.
func sessionIdentity(g *gin.Context) (*service.Identity, bool) {
	identity, ok := middleware.Identity(g)
	if !ok {
		// not covered: every route here sits behind SessionAuthMiddleware,
		// which aborts before the handler when there is no identity.
		handleError(g, &errorresponses.UnauthorizedError{})
	}
	return identity, ok
}

// shellsHandler handles GET /api/nss/shells.
//
// @Summary     The shells a person may choose
// @Description nss.allowed_shells, as bare names in configured order, for the login shell field.
// @Tags        web
// @Produce     json
// @Success     200 {object} openapidoc.NSSShellsEnvelope "The allowed shells"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     404 {object} openapidoc.ErrorEnvelope "nss_disabled: nss.enabled is false"
// @Security    sessionCookie
// @Router      /api/nss/shells [get]
func (k *nssKeyController) shellsHandler(g *gin.Context) {
	respondData(g, webtypes.NSSShellsResponse{AllowedShells: orEmpty(k.lookups.AllowedShells())})
}

// listHandler handles GET /api/nss/keys.
//
// @Summary     List fleet API keys
// @Description Every key for the fleet key admin role, admins, SOC and auditors; for anyone else, the keys
// @Description whose group they are in. Each key carries what the caller may do with it.
// @Tags        web
// @Produce     json
// @Success     200 {object} openapidoc.NSSKeyListEnvelope "The keys, by name"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     404 {object} openapidoc.ErrorEnvelope "nss_disabled: nss.enabled is false"
// @Security    sessionCookie
// @Router      /api/nss/keys [get]
func (k *nssKeyController) listHandler(g *gin.Context) {
	identity, ok := sessionIdentity(g)
	if !ok {
		return
	}
	views, err := k.keys.List(g.Request.Context(), identity)
	if err != nil {
		handleError(g, err)
		return
	}
	keys := make([]webtypes.NSSKeyResponse, 0, len(views))
	for i := range views {
		keys = append(keys, newNSSKeyResponse(&views[i]))
	}
	respondData(g, webtypes.NSSKeyListResponse{Keys: keys, CanCreate: k.keys.CanCreate(identity)})
}

// createHandler handles POST /api/nss/keys.
//
// @Summary     Create a fleet API key
// @Description Fleet key admin role and admins only. Returns the key and its secret; the secret is shown
// @Description this once and never again, so the caller must store it now. expires_at defaults to
// @Description nss.key_default_lifetime from now and notification_email to the creator's address.
// @Tags        web
// @Accept      json
// @Produce     json
// @Param       request body webtypes.CreateNSSKeyBody true "The new key"
// @Success     200 {object} openapidoc.NSSKeySecretEnvelope "The key and its secret"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Malformed body, a missing name, a past expiry or a bad address"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not the fleet key admin role, or no user record"
// @Failure     404 {object} openapidoc.ErrorEnvelope "nss_disabled: nss.enabled is false"
// @Security    sessionCookie
// @Router      /api/nss/keys [post]
func (k *nssKeyController) createHandler(g *gin.Context) {
	identity, ok := sessionIdentity(g)
	if !ok {
		return
	}
	var body webtypes.CreateNSSKeyBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "fleet key body was malformed"})
		return
	}
	view, secret, err := k.keys.Create(g.Request.Context(), identity, service.NSSKeyCreate{
		Name:              body.Name,
		Description:       body.Description,
		GroupName:         body.GroupName,
		ExpiresAt:         body.ExpiresAt,
		NotificationEmail: body.NotificationEmail,
	}, g.ClientIP())
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, webtypes.NSSKeySecretResponse{Key: newNSSKeyResponse(view), Secret: secret})
}

// getHandler handles GET /api/nss/keys/{id}.
//
// @Summary     One fleet API key
// @Description The same view as the list, for one key. A key the caller may not view is a 404.
// @Tags        web
// @Produce     json
// @Param       id path string true "Key id"
// @Success     200 {object} openapidoc.NSSKeyEnvelope "The key"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such key the caller may view, or nss_disabled"
// @Security    sessionCookie
// @Router      /api/nss/keys/{id} [get]
func (k *nssKeyController) getHandler(g *gin.Context) {
	identity, ok := sessionIdentity(g)
	if !ok {
		return
	}
	view, err := k.keys.Get(g.Request.Context(), identity, g.Param("id"))
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, newNSSKeyResponse(view))
}

// updateHandler handles PATCH /api/nss/keys/{id}.
//
// @Summary     Change a fleet API key
// @Description description, expires_at and notification_email need edit: the fleet key admin role, admins,
// @Description or the key's group. group_name needs assign_group: the fleet key admin role or admins. Fields
// @Description left out are unchanged. Moving expires_at re-arms the expiry reminder. A revoked key is a 409.
// @Tags        web
// @Accept      json
// @Produce     json
// @Param       id      path string                   true "Key id"
// @Param       request body webtypes.UpdateNSSKeyBody true "The change"
// @Success     200 {object} openapidoc.NSSKeyEnvelope "The key after the change"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Malformed body, nothing to change, a past expiry or a bad address"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "The caller may view the key but not make this change"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such key the caller may view, or nss_disabled"
// @Failure     409 {object} openapidoc.ErrorEnvelope "The key is revoked, or changed meanwhile"
// @Security    sessionCookie
// @Router      /api/nss/keys/{id} [patch]
func (k *nssKeyController) updateHandler(g *gin.Context) {
	identity, ok := sessionIdentity(g)
	if !ok {
		return
	}
	var body webtypes.UpdateNSSKeyBody
	if err := g.ShouldBindJSON(&body); err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "fleet key body was malformed"})
		return
	}
	view, err := k.keys.Update(g.Request.Context(), identity, g.Param("id"), service.NSSKeyUpdate{
		Description:       body.Description,
		ExpiresAt:         body.ExpiresAt,
		NotificationEmail: body.NotificationEmail,
		GroupName:         body.GroupName,
	}, g.ClientIP())
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, newNSSKeyResponse(view))
}

// rotateHandler handles POST /api/nss/keys/{id}/rotate.
//
// @Summary     Rotate a fleet API key
// @Description Issues a new secret, shown this once. The current secret becomes the previous one and stays
// @Description accepted for nss.key_rotation_grace, so hosts can switch over. The fleet key admin role,
// @Description admins, or the key's group. A revoked key is a 409.
// @Tags        web
// @Produce     json
// @Param       id path string true "Key id"
// @Success     200 {object} openapidoc.NSSKeySecretEnvelope "The key and its new secret"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "The caller may view the key but not rotate it"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such key the caller may view, or nss_disabled"
// @Failure     409 {object} openapidoc.ErrorEnvelope "The key is revoked, or was rotated meanwhile"
// @Security    sessionCookie
// @Router      /api/nss/keys/{id}/rotate [post]
func (k *nssKeyController) rotateHandler(g *gin.Context) {
	identity, ok := sessionIdentity(g)
	if !ok {
		return
	}
	view, secret, err := k.keys.Rotate(g.Request.Context(), identity, g.Param("id"), g.ClientIP())
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, webtypes.NSSKeySecretResponse{Key: newNSSKeyResponse(view), Secret: secret})
}

// clearPreviousHandler handles DELETE /api/nss/keys/{id}/previous.
//
// @Summary     End a fleet API key's previous secret early
// @Description Stops accepting the previous secret before its rotation grace ends, once every host has
// @Description switched. Who may rotate the key may do this. A key with no previous secret is a 409.
// @Tags        web
// @Produce     json
// @Param       id path string true "Key id"
// @Success     200 {object} openapidoc.NSSKeyEnvelope "The key after the change"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "The caller may view the key but not rotate it"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such key the caller may view, or nss_disabled"
// @Failure     409 {object} openapidoc.ErrorEnvelope "The key is revoked or has no previous secret"
// @Security    sessionCookie
// @Router      /api/nss/keys/{id}/previous [delete]
func (k *nssKeyController) clearPreviousHandler(g *gin.Context) {
	identity, ok := sessionIdentity(g)
	if !ok {
		return
	}
	view, err := k.keys.ClearPrevious(g.Request.Context(), identity, g.Param("id"), g.ClientIP())
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, newNSSKeyResponse(view))
}

// revokeHandler handles POST /api/nss/keys/{id}/revoke.
//
// @Summary     Revoke a fleet API key
// @Description Ends the key for good: neither secret is accepted again. The fleet key admin role, admins,
// @Description SOC, or the key's group. Already revoked is a 409.
// @Tags        web
// @Produce     json
// @Param       id path string true "Key id"
// @Success     200 {object} openapidoc.NSSKeyEnvelope "The revoked key"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     403 {object} openapidoc.ErrorEnvelope "The caller may view the key but not revoke it"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such key the caller may view, or nss_disabled"
// @Failure     409 {object} openapidoc.ErrorEnvelope "The key is already revoked"
// @Security    sessionCookie
// @Router      /api/nss/keys/{id}/revoke [post]
func (k *nssKeyController) revokeHandler(g *gin.Context) {
	identity, ok := sessionIdentity(g)
	if !ok {
		return
	}
	view, err := k.keys.Revoke(g.Request.Context(), identity, g.Param("id"), g.ClientIP())
	if err != nil {
		handleError(g, err)
		return
	}
	respondData(g, newNSSKeyResponse(view))
}

// usageHandler handles GET /api/nss/keys/{id}/usage.
//
// @Summary     A fleet API key's usage
// @Description Lookups and misses per source address, secret generation and UTC day, newest day first,
// @Description kept for nss.usage_retention. Counts are written once a minute, so the newest minute is not
// @Description in them yet. Whoever may view the key.
// @Tags        web
// @Produce     json
// @Param       id    path  string true  "Key id"
// @Param       after query string false "next_cursor of the previous page"
// @Param       limit query int    false "Page size, 1 to 100 (default 25)"
// @Success     200 {object} openapidoc.NSSKeyUsageEnvelope "One page of usage"
// @Failure     400 {object} openapidoc.ErrorEnvelope "A malformed limit or an unknown cursor"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such key the caller may view, or nss_disabled"
// @Security    sessionCookie
// @Router      /api/nss/keys/{id}/usage [get]
func (k *nssKeyController) usageHandler(g *gin.Context) {
	identity, ok := sessionIdentity(g)
	if !ok {
		return
	}
	params, err := paging.Parse(g.Request.URL.Query())
	if err != nil {
		handleError(g, err)
		return
	}
	page, err := k.keys.Usage(g.Request.Context(), identity, g.Param("id"), params)
	if err != nil {
		handleError(g, err)
		return
	}
	rows := make([]webtypes.NSSKeyUsageRowResponse, 0, len(page.Rows))
	for _, row := range page.Rows {
		rows = append(rows, webtypes.NSSKeyUsageRowResponse{
			SourceIP:   row.SourceIP,
			Generation: row.Generation,
			Day:        row.Day,
			Lookups:    row.Lookups,
			Misses:     row.Misses,
			FirstSeen:  row.FirstSeen,
			LastSeen:   row.LastSeen,
		})
	}
	respondData(g, webtypes.NSSKeyUsageResponse{Rows: rows, Total: page.Total, NextCursor: page.NextCursor})
}

// rotationReportHandler handles GET /api/nss/keys/{id}/rotation-report.
//
// @Summary     A fleet API key's rotation report
// @Description Per source address, whether it has switched to the current secret since the last rotation:
// @Description switched, not_switched (still presenting the previous secret, so it stops resolving accounts
// @Description when the grace ends) or gone (not seen since the rotation). With format=csv the rows come as
// @Description a CSV download instead. Whoever may view the key.
// @Tags        web
// @Produce     json
// @Produce     text/csv
// @Param       id     path  string true  "Key id"
// @Param       format query string false "csv for a CSV download" Enums(csv)
// @Success     200 {object} openapidoc.NSSRotationReportEnvelope "The report"
// @Failure     400 {object} openapidoc.ErrorEnvelope "An unknown format"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     404 {object} openapidoc.ErrorEnvelope "No such key the caller may view, or nss_disabled"
// @Security    sessionCookie
// @Router      /api/nss/keys/{id}/rotation-report [get]
func (k *nssKeyController) rotationReportHandler(g *gin.Context) {
	identity, ok := sessionIdentity(g)
	if !ok {
		return
	}
	format := g.Query("format")
	if format != "" && format != "csv" {
		handleError(g, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("format must be csv or absent, got %q", format)})
		return
	}
	report, err := k.keys.RotationReport(g.Request.Context(), identity, g.Param("id"))
	if err != nil {
		handleError(g, err)
		return
	}
	if format == "csv" {
		writeRotationReportCSV(g, report)
		return
	}

	response := webtypes.NSSRotationReportResponse{
		KeyID:             report.KeyID,
		CurrentGeneration: report.CurrentGeneration,
		RotatedAt:         report.RotatedAt,
		PreviousExpiresAt: report.PreviousExpiresAt,
		GraceDaysLeft:     report.GraceDaysLeft,
		Switched:          report.Switched,
		NotSwitched:       report.NotSwitched,
		Gone:              report.Gone,
		Rows:              make([]webtypes.NSSRotationReportRowResponse, 0, len(report.Rows)),
	}
	for _, row := range report.Rows {
		response.Rows = append(response.Rows, webtypes.NSSRotationReportRowResponse{
			SourceIP:       row.SourceIP,
			FirstSeen:      row.FirstSeen,
			LastSeen:       row.LastSeen,
			Lookups:        row.Lookups,
			LastGeneration: row.LastGeneration,
			Bucket:         row.Bucket,
		})
	}
	respondData(g, response)
}

// writeRotationReportCSV writes the report's rows as a CSV download, one
// address per line, in the report's order. Addresses and buckets never hold
// a formula prefix, so the cells need no spreadsheet escaping.
func writeRotationReportCSV(g *gin.Context, report *service.NSSRotationReport) {
	g.Header("Content-Type", "text/csv; charset=utf-8")
	g.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-rotation-report.csv"`, report.KeyID))
	g.Status(http.StatusOK)
	w := csv.NewWriter(g.Writer)
	records := [][]string{{"source_ip", "bucket", "last_generation", "first_seen", "last_seen", "lookups"}}
	for _, row := range report.Rows {
		records = append(records, []string{
			row.SourceIP,
			row.Bucket,
			strconv.Itoa(row.LastGeneration),
			row.FirstSeen.UTC().Format(time.RFC3339),
			row.LastSeen.UTC().Format(time.RFC3339),
			strconv.FormatInt(row.Lookups, 10),
		})
	}
	// not covered: a write to the response can only fail once the client
	// has gone, and then there is nobody to tell.
	_ = w.WriteAll(records) //nolint:errcheck // see above.
}

// newNSSKeyResponse converts a key view to its wire shape.
func newNSSKeyResponse(view *service.NSSKeyView) webtypes.NSSKeyResponse {
	key := view.Key
	return webtypes.NSSKeyResponse{
		ID:                key.ID,
		Name:              key.Name,
		Description:       key.Description,
		GroupName:         key.GroupName,
		Status:            view.Status,
		CurrentGeneration: key.CurrentGeneration,
		CreatedBy:         key.CreatedBy,
		CreatedByUsername: view.CreatedByUsername,
		CreatedAt:         key.CreatedAt,
		UpdatedAt:         key.UpdatedAt,
		ExpiresAt:         key.ExpiresAt,
		NotificationEmail: key.NotificationEmail,
		RotatedAt:         key.RotatedAt,
		PreviousExpiresAt: key.PreviousExpiresAt,
		RevokedAt:         key.RevokedAt,
		RevokedBy:         key.RevokedBy,
		RevokedByUsername: view.RevokedByUsername,
		Permissions:       orEmpty(view.Permissions),
	}
}
