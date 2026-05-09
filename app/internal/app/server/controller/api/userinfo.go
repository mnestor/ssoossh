package api

import (
	"net/http"

	oidc "github.com/coryschwartz/gin-oidc-client/handlers"
	"github.com/gin-gonic/gin"
)

func userinfoSelfHandler(c *gin.Context) {
	claims := c.GetStringMap(oidc.ClaimsKey)
	username := claims["sub"].(string)
	userinfo(username)
	c.JSON(http.StatusOK, claims)
}

func userinfoOtherHandler(c *gin.Context) {
	username := c.Param("username")
	userinfo(username)
}

func userinfo(u string) {}
