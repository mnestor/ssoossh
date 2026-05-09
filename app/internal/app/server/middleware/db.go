package middleware

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/model"
)

func NewDbMiddleware(db *gorm.DB) gin.HandlerFunc {
	m := model.NewModel(db)
	return func(c *gin.Context) {
		c.Set("model", &m)

		c.Next()
	}
}
