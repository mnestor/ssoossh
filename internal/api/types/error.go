// Created by Mike Nestor <me@mikenestor.org>
package types

type ResponseError struct {
	*ResponseRender
	StatusText string `json:"status"`
	Message    string `json:"message"`
}

func NewResponseError(s string, m string) *ResponseError {
	return &ResponseError{
		StatusText: s,
		Message:    m,
	}
}
