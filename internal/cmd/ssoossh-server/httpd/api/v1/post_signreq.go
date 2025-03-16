// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/render"
	"golang.org/x/crypto/ssh"

	types "github.com/mnestor/ssoossh/internal/api/response_types"
	mware "github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/store"
)

// apiSignRequestPost is sent by browser js with authenticated user
// will submit a signing request for approval to get a cert generated
func apiSignRequestPost(w http.ResponseWriter, r *http.Request) {
	data := &types.SignRequest{}
	if err := render.Bind(r, data); err != nil {
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	s := r.Context().Value(
		store.CertRequestContext).(*store.MemoryCertRequestStore)

	// validate that we have a valid PublicKey
	_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(data.PublicKey))
	if err != nil {
		render.Status(r, http.StatusBadRequest)
		_ = render.Render(w, r, types.ResponseError{
			StatusText: "fail",
			Message:    "please sent a valid public key",
		})
		return
	}

	cr := store.CertRequest{
		Pubkey:    data.PublicKey,
		CreatedAt: time.Now(),
		Type:      data.Type,
		Account:   data.Account,
	}

	// create sign request in db and return the uuid
	if err := s.Create(&cr); err != nil {
		render.Status(r, http.StatusBadRequest)
		_ = render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	mware.GetSession(r).Put(r.Context(), "signreq", cr.ID)
	resp := types.NewSignRequestResponse("success", cr.ID)

	slog.Info("got a signing request", slog.String("id", cr.ID), slog.String("pubkey", data.PublicKey))

	render.Status(r, http.StatusCreated)
	_ = render.Render(w, r, resp)
}
