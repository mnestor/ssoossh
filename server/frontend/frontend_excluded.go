//go:build exclude_frontend

package frontend

// Adapted from https://github.com/pocket-id/pocket-id/blob/main/backend/frontend/frontend_excluded.go

import "github.com/gin-gonic/gin"

// RegisterFrontend always returns ErrFrontendNotIncluded, since this binary
// was built with the exclude_frontend build tag.
func RegisterFrontend(router *gin.Engine) error {
	return ErrFrontendNotIncluded
}
