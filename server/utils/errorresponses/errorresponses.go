// Package errorresponses defines error types that carry an HTTP status
// code, for handlers to translate into API error responses.
package errorresponses

import (
	"fmt"
	"net/http"
)

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

// CertificateUnavailableError indicates a certificate request was approved
// and signed, but the signed certificate can no longer be obtained.
//
// Certificates are deliberately never persisted (see
// docs/signing-pipeline.md): they're delivered once, via an
// in-memory cache and its wake message. A client reconnecting after the
// server restarted has missed that window and must make a new request —
// which is cheap, since the certificates are short-lived by design.
//
// Rendered as 410 Gone rather than 404: the request genuinely existed and
// was approved, and 410 is also outside the client's SSE retry conditions
// (see internal/api/sse.go), so it stops rather than reconnect-loops.
type CertificateUnavailableError struct {
	// RequestID is the request whose certificate is no longer available.
	RequestID string
}

// Error implements the error interface.
func (e *CertificateUnavailableError) Error() string {
	return fmt.Sprintf("certificate for request %q is no longer available; please make a new request", e.RequestID)
}

// HTTPStatusCode reports the HTTP status this error should be rendered as.
func (e *CertificateUnavailableError) HTTPStatusCode() int { return http.StatusGone }

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

// ForbiddenError indicates the caller is authenticated but not permitted to
// act on this resource — as opposed to NotFoundError, which says the
// resource isn't there at all.
//
// Reason is returned to the caller, so keep it about authorization and free
// of anything that would confirm details about a resource they don't own.
type ForbiddenError struct {
	Reason string
}

// Error implements the error interface.
func (e *ForbiddenError) Error() string {
	if e.Reason == "" {
		return "forbidden"
	}
	return e.Reason
}

// HTTPStatusCode reports the HTTP status this error should be rendered as.
func (e *ForbiddenError) HTTPStatusCode() int { return http.StatusForbidden }
