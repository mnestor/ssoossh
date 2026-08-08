//go:build exclude_frontend

package frontend

import "github.com/gin-gonic/gin"

// RegisterFrontend always returns ErrFrontendNotIncluded, since this binary
// was built with the exclude_frontend build tag.
func RegisterFrontend(router *gin.Engine) error {
	return ErrFrontendNotIncluded
}
