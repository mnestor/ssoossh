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

// NotFoundError indicates the requested resource does not exist.
type NotFoundError struct {
	// Resource describes what wasn't found, e.g. `certificate request "abc"`.
	Resource string
}

// Error implements the error interface.
func (e *NotFoundError) Error() string {
	if e.Resource == "" {
		return "not found"
	}
	return e.Resource + " not found"
}

// HTTPStatusCode reports the HTTP status this error should be rendered as.
func (e *NotFoundError) HTTPStatusCode() int { return http.StatusNotFound }

// NotImplementedError indicates a route or check exists as a scaffold but
// its logic hasn't been implemented yet. Handlers/middleware that are still
// placeholders should fail closed with this rather than silently allowing
// the request through.
type NotImplementedError struct{}

// Error implements the error interface.
func (e *NotImplementedError) Error() string {
	return "Not implemented"
}

// HTTPStatusCode reports the HTTP status this error should be rendered as.
func (e *NotImplementedError) HTTPStatusCode() int { return http.StatusNotImplemented }
