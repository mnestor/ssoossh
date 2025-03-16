// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/render"

	types "github.com/mnestor/ssoossh/internal/api/response_types"
	"github.com/mnestor/ssoossh/internal/config"
)

var gConfig = config.GetConfig

func apiGetCAs(w http.ResponseWriter, r *http.Request) {
	key, err := gConfig().SshPubKey()
	if err != nil {
		render.Status(r, http.StatusInternalServerError)
		_ = render.Render(w, r, types.NewResponseError("signingkey", "unable to load signing key"))
		return
	}
	resp := types.NewCAListResponse("success", strings.Trim(key, "\n"))

	render.Status(r, http.StatusOK)
	_ = render.Render(w, r, resp)
}
