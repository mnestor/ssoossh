//go:build exclude_web

package web

import "github.com/gin-gonic/gin"

func RegisterFrontend(router *gin.Engine) error {
	return ErrFrontendNotIncluded
}
