package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// NewCertificateController registers the certificate-history and certificate-detail routes on
// group, behind sessionAuthMiddleware.
func NewCertificateController(
	group *gin.RouterGroup,
	certificateService service.CertificateProvider,
	sessionAuthMiddleware gin.HandlerFunc,
	cfg *config.Config,
) {
	cc := &certificateController{certificateService: certificateService, config: cfg}

	group.GET("/certs", sessionAuthMiddleware, cc.listHandler)
	group.GET("/certs/:id", sessionAuthMiddleware, cc.detailHandler)
}

// certificateController handles the issued-certificate history and detail routes.
type certificateController struct {
	certificateService service.CertificateProvider
	config             *config.Config
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
		handleError(g, &errorresponses.UnauthorizedError{})
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

// detailHandler handles GET /api/certs/:id: returns a single certificate by ID
// if the caller is authorized to view it.
//
// @Summary     View a certificate's full details
// @Description Returns a single certificate by ID if the caller is authorized:
// @Description the user who approved the underlying request, or someone with auditor-level access.
// @Description Returns 404 uniformly for "not found" and "not authorized" to not leak existence.
// @Tags        web
// @Produce     json
// @Param       id path string true "Certificate ID"
// @Success     200 {object} openapidoc.CertificateDetailEnvelope "Certificate details"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid certificate ID"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Failure     404 {object} openapidoc.ErrorEnvelope "Certificate not found or not accessible"
// @Security    sessionCookie
// @Router      /api/certs/{id} [get]
func (cc *certificateController) detailHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &errorresponses.UnauthorizedError{})
		return
	}

	id := g.Param("id")
	if id == "" {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "certificate ID is required"})
		return
	}

	certWithDecision, err := cc.certificateService.GetByID(g.Request.Context(), id, identity, cc.config)
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, newCertificateResponseFromWithDecision(certWithDecision))
}
