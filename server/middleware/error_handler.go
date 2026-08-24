package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// httpStatusCoder is implemented by errors that know which HTTP status they
// should be rendered as (e.g. errorresponses.TooManyRequestsError).
type httpStatusCoder interface {
	HTTPStatusCode() int
}

// errorCoder is implemented by errors that know their machine-readable error
// code (e.g. errorresponses.NotFoundError).
type errorCoder interface {
	ErrorCode() string
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
// status; anything else defaults to 500 Internal Server Error. Errors
// implementing errorCoder are assigned that code; others default to internal_error.
// If a handler already wrote a response, this is a no-op.
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

		var code string
		if coder, ok := err.(errorCoder); ok {
			code = coder.ErrorCode()
		} else {
			// Map HTTP status to error code for errors that don't implement errorCoder.
			code = statusToErrorCode(status)
		}

		c.JSON(status, gin.H{"data": nil, "error": err.Error(), "error_code": code})
	}
}

// statusToErrorCode maps an HTTP status code to an error code constant for
// errors that don't implement the errorCoder interface.
func statusToErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return apitypes.ErrorCodeInvalidRequest
	case http.StatusUnauthorized:
		return apitypes.ErrorCodeUnauthenticated
	case http.StatusForbidden:
		return apitypes.ErrorCodeForbidden
	case http.StatusNotFound:
		return apitypes.ErrorCodeNotFound
	case http.StatusGone:
		return apitypes.ErrorCodeUnavailable
	case http.StatusTooManyRequests:
		return apitypes.ErrorCodeRateLimited
	case http.StatusNotImplemented:
		return apitypes.ErrorCodeNotImplemented
	default:
		return apitypes.ErrorCodeInternalError
	}
}
