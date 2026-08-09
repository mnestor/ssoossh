package api

import (
	"encoding/json"
	"fmt"
	"io"

	"resty.dev/v3"
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

// decodeResponseError builds a *ResponseError from resp, whose status is
// already known to be non-2xx. Reads resp.Body directly when the request
// used SetResponseDoNotParse (streaming calls, where resty never buffers
// the body), falling back to resp.Bytes() for ordinary buffered
// responses. A body that isn't the expected {"error": "..."} shape (or is
// empty) still produces a ResponseError, just without a Message.
func decodeResponseError(resp *resty.Response) *ResponseError {
	respErr := &ResponseError{StatusCode: resp.StatusCode()}

	raw := resp.Bytes()
	if resp.Body != nil {
		if b, err := io.ReadAll(resp.Body); err == nil {
			raw = b
		}
	}

	var body errorBody
	if err := json.Unmarshal(raw, &body); err == nil {
		respErr.Message = body.Error
	}

	return respErr
}
