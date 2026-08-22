package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
)

// NewCertificateController registers the certificate-history route on
// group, behind sessionAuthMiddleware.
func NewCertificateController(group *gin.RouterGroup, certificateService service.CertificateProvider, sessionAuthMiddleware gin.HandlerFunc) {
	cc := &certificateController{certificateService: certificateService}

	group.GET("/certs", sessionAuthMiddleware, cc.listHandler)
}

// certificateController handles the issued-certificate history routes.
type certificateController struct {
	certificateService service.CertificateProvider
}

// Scoping is the service's job (see CertificateService.ListForIdentity) and
// is by users row, not by anything in the certificate — so there is no
// query parameter here to widen it. A user cannot ask for someone else's
// history because there is nothing to ask with.
// listHandler returns the caller's own issued certificates using cursor-based
// pagination. Query parameters: after (cursor, optional), limit (default 25, max 100).
//
// @Summary     The caller's issued-certificate history
// @Description Metadata only. Certificates themselves are ephemeral and never persisted,
// @Description so this is an audit trail rather than somewhere to re-download one.
// @Description
// @Description Scoped by the caller's users row, with no parameter to widen it.
// @Description Uses cursor-based pagination (not offset).
// @Tags        web
// @Produce     json
// @Param       after   query    string false   "Certificate ID to start after (cursor)"
// @Param       limit   query    int    false   "Maximum certificates to return (default 25, max 100)"
// @Success     200 {object} openapidoc.CertificateListEnvelope "Issued certificates, newest first, with cursor for next page"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid limit or cursor"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Security    sessionCookie
// @Router      /api/certs [get]
func (cc *certificateController) listHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &middleware.UnauthorizedError{})
		return
	}

	// Parse pagination parameters.
	var after *string
	if afterStr := g.Query("after"); afterStr != "" {
		after = &afterStr
	}

	// Parse limit with defaults and bounds.
	const defaultPageSize = 25
	const maxPageSize = 100
	limit := defaultPageSize
	if limitStr := g.Query("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			if parsed > maxPageSize {
				parsed = maxPageSize
			}
			limit = parsed
		}
	}

	certs, nextCursor, err := cc.certificateService.ListForIdentity(g.Request.Context(), identity, after, limit)
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, newCertificateListResponse(certs, nextCursor))
}
