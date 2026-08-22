package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewBrandingController registers branding routes.
// This must be registered before session auth middleware so the login page
// can fetch branding before a session exists.
func NewBrandingController(group *gin.RouterGroup, cfg *config.Config) {
	b := &brandingController{config: cfg}
	group.GET("/branding", b.getBrandingHandler)
}

// brandingController handles branding-related HTTP routes.
type brandingController struct {
	config *config.Config
}

// getBrandingHandler handles GET /branding, returning optional branding configuration.
//
// @Summary     Get branding configuration
// @Description Unauthenticated endpoint that returns optional branding for the login page
// @Description and web UI. All fields are optional; empty values mean no branding is
// @Description configured. Returns an empty object if no branding is configured.
// @Tags        public
// @Produce     json
// @Success     200 {object} openapidoc.BrandingEnvelope "Branding configuration (may be empty)"
// @Router      /api/branding [get]
func (b *brandingController) getBrandingHandler(gc *gin.Context) {
	resp := webtypes.BrandingResponse{
		OrgName:     b.config.Branding.OrgName,
		LogoURL:     b.config.Branding.LogoURL,
		LoginNotice: b.config.Branding.LoginNotice,
	}

	respondData(gc, resp)
}
