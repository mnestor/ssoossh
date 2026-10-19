package errorresponses

import (
	"net/http"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// NSSError is a failed answer from the NSS endpoints, carrying one of
// internal/apitypes' ErrorCodeNSS* codes rather than a general one. The
// ssoossh-nss daemon acts on the difference: only nss_not_found is an
// authoritative miss it may cache, so a bad request, a disabled server or a
// refused key must never be rendered with that code, and none of them with
// a code the daemon cannot tell apart from it.
//
// Message is returned to the caller, so it never names the key secret, and
// the 401 message is the same for every cause (see NSSUnauthorized).
type NSSError struct {
	// Status is the HTTP status the error renders with.
	Status int
	// Code is the envelope's error_code, an apitypes.ErrorCodeNSS* value.
	Code string
	// Message is the caller-safe description.
	Message string
}

// Error implements the error interface.
func (e *NSSError) Error() string { return e.Message }

// HTTPStatusCode reports the HTTP status this error should be rendered as.
func (e *NSSError) HTTPStatusCode() int { return e.Status }

// ErrorCode reports the machine-readable error code.
func (e *NSSError) ErrorCode() string { return e.Code }

// NSSNotFound is the authoritative miss: no servable user or group has that
// name or id. The one answer a host may negative-cache.
func NSSNotFound() *NSSError {
	return &NSSError{Status: http.StatusNotFound, Code: apitypes.ErrorCodeNSSNotFound, Message: "no such user or group"}
}

// NSSBadRequest is a name or id that failed validation, so it was never
// looked up. reason says which rule it broke; it describes the caller's own
// input and nothing about the directory.
func NSSBadRequest(reason string) *NSSError {
	return &NSSError{Status: http.StatusBadRequest, Code: apitypes.ErrorCodeNSSBadRequest, Message: reason}
}

// NSSDisabled is every NSS route while nss.enabled is false. It says
// nothing about whether a name exists.
func NSSDisabled() *NSSError {
	return &NSSError{Status: http.StatusNotFound, Code: apitypes.ErrorCodeNSSDisabled, Message: "the NSS directory is not enabled on this server"}
}

// NSSUnauthorized is a missing, unknown, revoked, expired or wrong fleet
// key. One message for every cause, so the body never says which.
func NSSUnauthorized() *NSSError {
	return &NSSError{Status: http.StatusUnauthorized, Code: apitypes.ErrorCodeNSSUnauthorized, Message: "fleet key not accepted"}
}

// NSSRateLimited is an empty rate limit bucket. The caller sets
// Retry-After alongside it.
func NSSRateLimited() *NSSError {
	return &NSSError{Status: http.StatusTooManyRequests, Code: apitypes.ErrorCodeNSSRateLimited, Message: "too many lookups; retry after the Retry-After interval"}
}

// NSSForbidden is a connection from outside nss.source_allow_list, refused
// before the key was looked at.
func NSSForbidden() *NSSError {
	return &NSSError{Status: http.StatusForbidden, Code: apitypes.ErrorCodeNSSForbidden, Message: "lookups are not accepted from this address"}
}
