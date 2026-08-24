package controller

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// NewVersionController registers the version route.
// Like branding, this must be registered before session auth middleware:
// the footer renders on the login page too, before any session exists.
func NewVersionController(group *gin.RouterGroup) {
	group.GET("/version", getVersionHandler)
}

// getVersionHandler handles GET /version, returning the running build's identity.
//
// @Summary     Get server version
// @Description Unauthenticated endpoint that returns the running server's build
// @Description identity for display in the web UI footer. Values come from the
// @Description build stamp; an untagged build reports "development" and omits the
// @Description release URL.
// @Tags        public
// @Produce     json
// @Success     200 {object} openapidoc.VersionEnvelope "Build identity of the running server"
// @Router      /api/version [get]
func getVersionHandler(gc *gin.Context) {
	respondData(gc, webtypes.VersionResponse{
		Version:    version.Version,
		Commit:     version.Commit,
		GithubURL:  version.Github,
		ReleaseURL: releaseURL(version.Github, version.Version),
	})
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
