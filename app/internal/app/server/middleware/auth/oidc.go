package auth

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mnestor/ssoossh/internal/app/server/config"
	"github.com/mnestor/ssoossh/internal/common/oidc_client"
)

type Oidc struct {
	*oidc_client.OauthHandlers
}

func NewOidcMiddleware(cfg *config.Config) *Oidc {
	oh, _ := oidc_client.NewOauthHandlers(
		cfg.AuthConfig.ProviderUrl,
		cfg.AuthConfig.ClientID,
		cfg.AuthConfig.ClientSecret,
		strings.TrimSuffix(cfg.AppUrl, "/")+"/oauth/redirect",
		cfg.AuthConfig.Scopes,
	)

	return &Oidc{oh}
}

func (oh *Oidc) RegisterHandlers(r *gin.Engine, g string) {
	oauth := r.Group(g)
	{
		oauth.GET("/login", oh.HandleLogin)
		oauth.GET("/redirect", oh.HandleRedirect)
		oauth.GET("/logout", oh.HandleLogout)
	}
}

func (oh *Oidc) RequireLogin() gin.HandlerFunc {
	return oh.MiddlewareRequireLogin("")
}
