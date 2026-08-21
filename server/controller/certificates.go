package controller

import (
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
// listHandler returns the caller's own issued certificates.
//
// @Summary     The caller's issued-certificate history
// @Description Metadata only. Certificates themselves are ephemeral and never persisted,
// @Description so this is an audit trail rather than somewhere to re-download one.
// @Description
// @Description Scoped by the caller's users row, with no parameter to widen it.
// @Tags        web
// @Produce     json
// @Success     200 {object} openapidoc.CertificateListEnvelope "Issued certificates, newest first"
// @Failure     401 {object} openapidoc.ErrorEnvelope "No valid session"
// @Security    sessionCookie
// @Router      /api/certs [get]
func (cc *certificateController) listHandler(g *gin.Context) {
	identity, ok := middleware.Identity(g)
	if !ok {
		handleError(g, &middleware.UnauthorizedError{})
		return
	}

	certs, err := cc.certificateService.ListForIdentity(g.Request.Context(), identity)
	if err != nil {
		handleError(g, err)
		return
	}

	respondData(g, newCertificateResponses(certs))
}
