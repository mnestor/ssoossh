// Created by Mike Nestor <me@mikenestor.org>
package types

import "net/http"

type ResponseRender struct{}
type ResponseBase struct {
	*ResponseRender
	StatusText string `json:"status"`
}

func (e *ResponseRender) Render(w http.ResponseWriter, r *http.Request) error {
	return nil
}
