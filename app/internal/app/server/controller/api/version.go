package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/common"
)

func versionEndpoint(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"version": common.Version})
}
