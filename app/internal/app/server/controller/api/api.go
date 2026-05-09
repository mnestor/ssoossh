package api

import (
	"github.com/gin-gonic/gin"
)

func RegisterNoAuthHandlers(r *gin.RouterGroup) {
	r.GET("/version", versionEndpoint)
}

func RegisterAuthHandlers(r *gin.RouterGroup) {
	r.GET("/users/me", userinfoSelfHandler)
	r.GET("/users/:username", userinfoOtherHandler)
}
