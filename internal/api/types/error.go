// Created by Mike Nestor <me@mikenestor.org>
package types

type ResponseError struct {
	*ResponseBase
	Message string `json:"message"`
}

func NewResponseError(s string, m string) *ResponseError {
	return &ResponseError{
		ResponseBase: &ResponseBase{StatusText: s},
		Message:      m,
	}
}
