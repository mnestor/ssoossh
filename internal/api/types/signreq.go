// Created by Mike Nestor <me@mikenestor.org>
package types

import (
	"errors"
	"net/http"
)

type SignRequest struct {
	PublicKey string `json:"pubkey"`
	Type      string `json:"type"`
	Account   string `json:"username"`
}

type SignRequestResponse struct {
	*ResponseRender
	StatusText string `json:"status"`
	ID         string `json:""`
}

func NewSignRequestResponse(s string, id string) *SignRequestResponse {
	return &SignRequestResponse{
		StatusText: s,
		ID:         id,
	}
}

func (a *SignRequest) Bind(r *http.Request) error {
	if a.PublicKey == "" {
		return errors.New("missing required signrequest fields")
	}

	// just a post-process after a decode..
	return nil
}
