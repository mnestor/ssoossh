package common

import (
	"net/http"
)

type AppError interface {
	error
	HttpStatusCode() int
}

type TooManyRequestsError struct{}

func (e *TooManyRequestsError) Error() string {
	return "Too many requests"
}
func (e *TooManyRequestsError) HttpStatusCode() int { return http.StatusTooManyRequests }
