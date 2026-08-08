// Package errorresponses defines error types that carry an HTTP status
// code, for handlers to translate into API error responses.
package errorresponses

import "net/http"

// TooManyRequestsError indicates a client has exceeded a rate limit.
type TooManyRequestsError struct{}

// Error implements the error interface.
func (e *TooManyRequestsError) Error() string {
	return "Too many requests"
}

// HTTPStatusCode reports the HTTP status this error should be rendered as.
func (e *TooManyRequestsError) HTTPStatusCode() int { return http.StatusTooManyRequests }

// MisdirectedRequestError indicates a request addressed to a server name
// this server is not configured to answer for.
type MisdirectedRequestError struct{}

// Error implements the error interface.
func (e *MisdirectedRequestError) Error() string {
	return "Misdirected request"
}

// HTTPStatusCode reports the HTTP status this error should be rendered as.
func (e *MisdirectedRequestError) HTTPStatusCode() int { return http.StatusMisdirectedRequest }
