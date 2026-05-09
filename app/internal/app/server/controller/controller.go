package controller

import (
	"github.com/gin-gonic/gin"
)

func RegisterHandlers(r *gin.RouterGroup) {
	r.GET("/healthz", healthEndpoint)
	// r.GET("/test", func(c *gin.Context) {
	// 	s := sessions.DefaultMany(c, "login")
	//   s.Get("csrf")
	// })
}
