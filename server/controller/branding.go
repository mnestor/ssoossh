package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewBrandingController registers branding routes.
// logoImg is the loaded and validated logo image, or nil if no logo is configured.
// This must be registered before session auth middleware so the login page
// can fetch branding before a session exists.
func NewBrandingController(group *gin.RouterGroup, cfg *config.Config, logoImg *logoImage) {
	b := &brandingController{config: cfg, logo: logoImg}
	group.GET("/branding", b.getBrandingHandler)
	if logoImg != nil {
		group.GET("/branding/logo", b.getLogoHandler)
	}
}

// brandingController handles branding-related HTTP routes.
type brandingController struct {
	config *config.Config
	logo   *logoImage
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
		LoginNotice: b.config.Branding.LoginNotice,
	}

	// Only include logo_url if a logo is configured
	if b.logo != nil {
		resp.LogoURL = "/api/branding/logo"
	}

	respondData(gc, resp)
}

// getLogoHandler serves the logo image bytes with proper caching and security headers.
// This handler is only registered if a logo is configured.
//
// @Summary     Get organization logo image
// @Description Serves the logo image configured by the operator. The image is read
// @Description and validated at startup, so this handler simply streams the bytes with
// @Description appropriate cache headers.
// @Tags        public
// @Produce     image/png,image/jpeg,image/gif,image/webp,image/svg+xml
// @Success     200 {file} binary "Logo image"
// @Router      /api/branding/logo [get]
func (b *brandingController) getLogoHandler(gc *gin.Context) {
	// Serve with ETag for cache validation
	gc.Header("ETag", b.logo.etag)
	gc.Header("Content-Type", b.logo.contentType)

	// For SVG, add CSP to prevent script execution when accessed directly.
	// Inside an <img> tag, the page's CSP applies. When accessed directly as
	// a document, this header prevents <script> tags in the SVG from executing.
	if b.logo.contentType == "image/svg+xml" {
		gc.Header("Content-Security-Policy", "default-src 'none'")
	}

	// Set cache control: allow browsers and intermediate caches to cache for 7 days.
	// The logo is immutable at runtime (read at startup), so aggressive caching is safe.
	gc.Header("Cache-Control", "public, max-age=604800") // 7 days

	// If the client has the ETag, return 304 Not Modified
	if gc.GetHeader("If-None-Match") == b.logo.etag {
		gc.Status(http.StatusNotModified)
		return
	}

	gc.Data(http.StatusOK, b.logo.contentType, b.logo.bytes)
}
