package controller

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// versionController serves the build-identity endpoint. It holds the
// disclosure policy so the handler can decide how much of the real build to
// reveal (see config.VersionConfig).
type versionController struct {
	disclosure config.VersionConfig
}

// NewVersionController registers the version route.
// Like branding, this must be registered before session auth middleware:
// the footer renders on the login page too, before any session exists. The
// disclosure policy is unauthenticated for the same reason.
func NewVersionController(group *gin.RouterGroup, disclosure config.VersionConfig) {
	vc := &versionController{disclosure: disclosure}
	group.GET("/version", vc.getVersionHandler)
}

// getVersionHandler handles GET /version, returning the running build's identity
// filtered through the configured disclosure mode.
//
// @Summary     Get server version
// @Description Unauthenticated endpoint that returns the running server's build
// @Description identity for display in the web UI footer. What it reveals is
// @Description governed by version.mode: "show" (the real build), "hide"
// @Description (nothing), or "fake" (a fixed string). Values come from the
// @Description build stamp; an untagged build reports "development" and omits the
// @Description release URL.
// @Tags        public
// @Produce     json
// @Success     200 {object} openapidoc.VersionEnvelope "Build identity of the running server"
// @Router      /api/version [get]
func (vc *versionController) getVersionHandler(gc *gin.Context) {
	respondData(gc, versionResponse(vc.disclosure))
}

// releaseURL builds the GitHub release page URL for a stamped version, or
// returns "" when there is no release to point at.
//
// Only a version starting with a digit is treated as a release: the build
// stamp is a bare semver ("0.1.0") because both goreleaser and the Makefile
// strip the tag's leading "v", while an unstamped build carries the word
// "development". The "v" is put back because that is the tag the release
// lives under.
func releaseURL(github, v string) string {
	v = strings.TrimPrefix(v, "v")
	if github == "" || v == "" || v[0] < '0' || v[0] > '9' {
		return ""
	}
	return github + "/releases/tag/v" + v
}

// Response resolves the wire payload for the /api/version endpoint under the
// configured disclosure mode. It lives here, not in the config package, so
// the mode decision and the release-URL construction stay in one place; the
// config package holds only policy, not the build stamp it is applied to.
func versionResponse(d config.VersionConfig) webtypes.VersionResponse {
	switch d.Mode {
	case config.VersionHide:
		// Nothing identifying. Every field empty: the footer renders no
		// build line at all.
		return webtypes.VersionResponse{}
	case config.VersionShow, "":
		// The real build. Empty is treated as "show" so an unset mode keeps
		// the historical behaviour.
		return webtypes.VersionResponse{
			Version:    version.Version,
			Commit:     version.Commit,
			GithubURL:  version.Github,
			ReleaseURL: releaseURL(version.Github, version.Version),
		}
	default:
		// Any other value is taken literally as the version to display, and
		// nothing else is revealed -- no commit, no repository, no release
		// link that would give the real build away.
		return webtypes.VersionResponse{Version: d.Mode}
	}
}
