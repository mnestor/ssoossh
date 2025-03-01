// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"net/http"

	"github.com/go-chi/render"

	"github.com/mnestor/ssoossh/internal/api/types"
	"github.com/mnestor/ssoossh/internal/config"
)

func init() {
	router.Get("/ca", apiGetCAs)
}

func apiGetCAs(w http.ResponseWriter, r *http.Request) {
	key := config.GetConfig().SshPubKey()
	resp := types.NewCAListResponse("success", key)

	render.Status(r, http.StatusOK)
	_ = render.Render(w, r, resp)
}
