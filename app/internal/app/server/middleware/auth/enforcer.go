package auth

import (
	_ "embed"
	"log/slog"
	"net/http"
	"strings"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	oidc "github.com/coryschwartz/gin-oidc-client/handlers"
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/app/server/config"
)

type Enforcer struct {
	*casbin.Enforcer
	adminGroup string
}

//go:embed model.conf
var _model string

//go:embed policy.csv
var _policy string

func NewEnforcer(cfg *config.Config) *Enforcer {
	modelFromString, err := model.NewModelFromString(_model)
	if err != nil {
		slog.Error("failed to load model", slog.Any("error", err))
	}
	enforcer, err := casbin.NewEnforcer(modelFromString)
	if err != nil {
		slog.Error("failed to enforcer model", slog.Any("error", err))
	}
	loadPolicy(enforcer, _policy)

	return &Enforcer{enforcer, cfg.AuthConfig.AdminGroup}
}

func loadPolicy(enforcer *casbin.Enforcer, p string) {
	m := enforcer.GetModel()

	strs := strings.Split(p, "\n")
	for _, line := range strs {
		l := strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_ = persist.LoadPolicyLine(l, m)
	}
}

func (e *Enforcer) CheckAccessHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := c.GetStringMap(oidc.ClaimsKey)

		user := ""
		if len(claims) > 0 {
			user = claims["sub"].(string)
			// every user gets "default" role
			e.AddRoleForUser(user, "default")

			groups, ok := claims["groups"].([]interface{})
			if ok && len(e.adminGroup) >= 0 {
				for _, g := range groups {
					// not sure about this part
					// e.AddRoleForUser(user, g.(string))

					if g == e.adminGroup {
						e.AddRoleForUser(user, "global ssoossh admin")
						break
					}
				}
			}
		}

		canAccess, _ := e.Enforce(user, c.Request.URL.Path, c.Request.Method)
		if !canAccess {
			c.AbortWithStatus(http.StatusForbidden)
		}
		c.Next()
	}
}
