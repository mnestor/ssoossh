package api

import (
	"fmt"

	"resty.dev/v3"
)

// errorBody mirrors server/middleware.ErrorHandlerMiddleware's response
// shape: {"data": null, "error": "<message>"}. Registered client-wide via
// resty.Client.SetResultError, so resty auto-unmarshals it into
// resp.ResultError() for any ordinary (buffered) non-2xx response.
type errorBody struct {
	Error string `json:"error"`
}

// ResponseError is returned when ssoosshd responds with a non-2xx status.
// StatusCode and Message let callers branch on the specific failure (e.g.
// a 401 needing re-authentication vs. a 500) without string-matching
// Error().
type ResponseError struct {
	StatusCode int
	Message    string
}

// Error implements the error interface.
func (e *ResponseError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("ssoosshd returned status %d", e.StatusCode)
	}
	return fmt.Sprintf("ssoosshd returned status %d: %s", e.StatusCode, e.Message)
}

// decodeResponseError builds a *ResponseError from resp, whose status is
// already known to be non-2xx. resty has already unmarshalled the body into
// resp.ResultError() via the client-wide SetResultError(&errorBody{}) — no
// call in this package uses SetResponseDoNotParse anymore (the events
// stream is read separately via resty's SSESource in sse.go, not off a
// resty.Response), so that's always the case. A body that isn't the
// expected {"error": "..."} shape (or is empty) still produces a
// ResponseError, just without a Message.
func decodeResponseError(resp *resty.Response) *ResponseError {
	respErr := &ResponseError{StatusCode: resp.StatusCode()}

	if body, ok := resp.ResultError().(*errorBody); ok && body != nil {
		respErr.Message = body.Error
	}

	return respErr
}
