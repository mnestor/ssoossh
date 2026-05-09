package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func healthEndpoint(c *gin.Context) {
	c.Status(http.StatusNoContent)
}
