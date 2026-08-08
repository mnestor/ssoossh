package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/service"
)

// NewHostController registers the host-certificate routes that
// authenticate via an existing host certificate rather than a fresh OIDC
// approval, behind hostCertAuthMiddleware. First issuance (`host sign`) is
// registered separately by NewCertRequestController, since it goes through
// the OIDC approval chain instead.
func NewHostController(group *gin.RouterGroup, hostService service.HostProvider, hostCertAuthMiddleware gin.HandlerFunc) {
	h := &hostController{hostService: hostService}

	hostGroup := group.Group("", hostCertAuthMiddleware)
	hostGroup.POST("/certs/host/renew", h.renewHandler)

	// TODO: assuming sync also requires host-cert auth since the mapping
	// discloses which local accounts a cert's principals resolve to — not
	// an explicit requirement in docs/ssoossh-context.md, revisit if that's
	// too strict for first-time-sync bootstrapping.
	hostGroup.GET("/hosts/:hostname/sync", h.syncHandler)
}

// hostController handles the host-certificate HTTP routes.
type hostController struct {
	hostService service.HostProvider
}

// renewHostRequestBody is the POST /api/certs/host/renew request body.
type renewHostRequestBody struct {
	PublicKey string `json:"public_key" binding:"required"`
}

// renewHandler handles POST /api/certs/host/renew: reissues a host
// certificate, authenticated by the existing valid host certificate (see
// middleware.HostCertAuthMiddleware) rather than a fresh OIDC approval.
//
// TODO: read hostname and the presented existing certificate from
// middleware.HostnameContextKey / wherever HostCertAuthMiddleware ends up
// storing them, once implemented.
func (h *hostController) renewHandler(g *gin.Context) {
	var body renewHostRequestBody
	if err := g.ShouldBindJSON(&body); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	certificate, err := h.hostService.Renew(g.Request.Context(), "" /* TODO: hostname from context */, "" /* TODO: existing cert from context */, body.PublicKey)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusOK, gin.H{"certificate": certificate})
}

// syncHandler handles GET /api/hosts/:hostname/sync: returns the current
// principal mapping for `host sync` to write locally, for sshd's
// AuthorizedPrincipalsCommand to answer from without touching the network.
func (h *hostController) syncHandler(g *gin.Context) {
	principals, err := h.hostService.SyncPrincipals(g.Request.Context(), g.Param("hostname"))
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.JSON(http.StatusOK, gin.H{"principals": principals})
}
