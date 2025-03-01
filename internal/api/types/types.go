// Created by Mike Nestor <me@mikenestor.org>
package types

import "net/http"

type ResponseBase struct {
	StatusText string `json:"status"`
}

func (e *ResponseBase) Render(w http.ResponseWriter, r *http.Request) error {
	return nil
}
