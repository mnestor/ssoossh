package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// errorBody mirrors server/middleware.ErrorHandlerMiddleware's response
// shape: {"data": null, "error": "<message>"}.
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

// IsNotFound reports whether this is a 404 not-found error.
func (e *ResponseError) IsNotFound() bool {
	return e != nil && e.StatusCode == 404
}

// decodeResponseError builds a *ResponseError from a response whose status
// is already known to be non-2xx, and its body as already read. A body that
// isn't the expected {"error": "..."} shape (or is empty) still produces a
// ResponseError, just without a Message: the status alone is worth
// reporting, and a server that answered with an HTML error page should not
// turn into a decode error that hides it.
func decodeResponseError(statusCode int, body []byte) *ResponseError {
	respErr := &ResponseError{StatusCode: statusCode}

	var decoded errorBody
	if err := json.Unmarshal(body, &decoded); err == nil {
		respErr.Message = decoded.Error
	}

	return respErr
}

// responseError is decodeResponseError for a response nobody has read yet —
// the events stream's failed connect (see sse.go), where the body is still
// on the wire. Reading it is best-effort for the same reason as above.
func responseError(resp *http.Response) *ResponseError {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		// not covered: reaching this needs the connection to fail between
		// the response header arriving and its (already-buffered, in every
		// test) error body being read.
		return &ResponseError{StatusCode: resp.StatusCode}
	}
	return decodeResponseError(resp.StatusCode, body)
}

// maxErrorBodyBytes caps how much of a failed response is read looking for
// an error message. ssoosshd's error bodies are one short JSON object;
// anything larger is something else answering, and none of it belongs in
// sudo's memory.
const maxErrorBodyBytes = 64 << 10
