package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// httpStatusCoder is implemented by errors that know which HTTP status they
// should be rendered as (e.g. errorresponses.TooManyRequestsError).
type httpStatusCoder interface {
	HTTPStatusCode() int
}

// ErrorHandlerMiddleware translates errors registered on the gin context via
// c.Error into an HTTP response. Gin does not do this on its own: c.Error
// only records the error, so without this middleware a handler that aborts
// via c.Error+c.Abort produces an empty response with whatever status was
// last written (or 200, if none was).
type ErrorHandlerMiddleware struct{}

// NewErrorHandlerMiddleware creates an ErrorHandlerMiddleware.
func NewErrorHandlerMiddleware() *ErrorHandlerMiddleware {
	return &ErrorHandlerMiddleware{}
}

// Add returns a gin.HandlerFunc that, once the rest of the chain has run,
// checks for a registered error and writes the corresponding HTTP status
// and a JSON error body. Errors implementing httpStatusCoder use that
// status; anything else defaults to 500 Internal Server Error. If a handler
// already wrote a response, this is a no-op.
func (m *ErrorHandlerMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if c.Writer.Written() || len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err

		status := http.StatusInternalServerError
		if coder, ok := err.(httpStatusCoder); ok {
			status = coder.HTTPStatusCode()
		}

		c.JSON(status, gin.H{"data": nil, "error": err.Error()})
	}
}

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

// UnauthorizedError indicates a request is missing, or presented an
// incorrect, API key.
type UnauthorizedError struct{}

// Error implements the error interface.
func (e *UnauthorizedError) Error() string {
	return "Unauthorized"
}

// HTTPStatusCode reports the HTTP status this error should be rendered as.
func (e *UnauthorizedError) HTTPStatusCode() int { return http.StatusUnauthorized }

// ForbiddenError indicates a request is authenticated but not authorized to
// access the requested resource (e.g., missing required group membership).
type ForbiddenError struct{}

// Error implements the error interface.
func (e *ForbiddenError) Error() string {
	return "Forbidden"
}

// HTTPStatusCode reports the HTTP status this error should be rendered as.
func (e *ForbiddenError) HTTPStatusCode() int { return http.StatusForbidden }
