// Package controller provides HTTP handlers for the server.
package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/service"
)

// NewCaController registers ca routes.
func NewCaController(group *gin.RouterGroup, caService service.CAPublicKeyProvider) {
	ca := &caController{caService: caService}
	group.GET("/ca", ca.getCAHandler)
}

// caController handles the CA-related HTTP routes.
type caController struct {
	caService service.CAPublicKeyProvider
}

// getCAHandler handles GET /ca, returning the CA's public key.
//
// @Summary     The CA public key
// @Description For `TrustedUserCAKeys` and `@cert-authority` lines. Public by design —
// @Description it is a public key.
// @Tags        client
// @Produce     json
// @Success     200 {object} openapidoc.CAEnvelope "The CA public key in authorized_keys form"
// @Router      /api/ca [get]
func (c *caController) getCAHandler(g *gin.Context) {
	cakey, err := c.caService.GetCAPublicKey(g.Request.Context())
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	// utils.SetCacheControlHeader(c, 5*time.Minute, 15*time.Minute)

	respondData(g, apitypes.CAResponse{CA: cakey})
}
