// Package controller provides HTTP handlers for the server.
package controller

import (
	"net/http"

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
func (c *caController) getCAHandler(g *gin.Context) {
	cakey, err := c.caService.GetCAPublicKey(g.Request.Context())
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	// utils.SetCacheControlHeader(c, 5*time.Minute, 15*time.Minute)

	g.JSON(http.StatusOK, apitypes.CAResponse{CA: cakey})
}
