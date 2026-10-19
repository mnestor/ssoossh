package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// NSSClient is the set of NSS directory lookups ssoosshd-nss makes, each
// authenticated by the fleet key in Config.KeyID and Config.KeySecret.
// HTTPClient implements it. Kept apart from Client so the certificate
// client's fakes do not have to grow four methods they never use.
type NSSClient interface {
	// GetNSSUserByName looks a user up by login name, matched byte for
	// byte.
	GetNSSUserByName(ctx context.Context, name string) (*apitypes.NSSUserResponse, error)
	// GetNSSUserByUID looks a user up by uid.
	GetNSSUserByUID(ctx context.Context, uid uint32) (*apitypes.NSSUserResponse, error)
	// GetNSSGroupByName looks a group up by name, matched byte for byte.
	GetNSSGroupByName(ctx context.Context, name string) (*apitypes.NSSGroupResponse, error)
	// GetNSSGroupByGID looks a group up by gid.
	GetNSSGroupByGID(ctx context.Context, gid uint32) (*apitypes.NSSGroupResponse, error)
}

var _ NSSClient = (*HTTPClient)(nil)

// How a failed lookup is classified. Test with errors.Is; errors.As with
// *NSSError gives the status, the error code and Retry-After.
var (
	// ErrNSSNotFound is the server's authoritative miss: 404 with
	// nss_not_found, the one answer a host may negative-cache. A 404 with
	// nss_disabled, or a 404 from something other than ssoosshd, is not
	// this.
	ErrNSSNotFound = errors.New("no such NSS user or group")
	// ErrNSSUnauthorized is a 401: the fleet key is unknown, revoked,
	// expired or wrong, and the server does not say which.
	ErrNSSUnauthorized = errors.New("the server refused the fleet key")
	// ErrNSSRateLimited is a 429. NSSError.RetryAfter says how long the
	// server asked the host to wait.
	ErrNSSRateLimited = errors.New("the server is rate limiting this fleet key")
	// ErrNSSNoKey is a lookup on a client built without a fleet key. No
	// request is sent.
	ErrNSSNoKey = errors.New("an NSS lookup needs a fleet key (KeyID and KeySecret)")
)

// NSSError is a non-2xx answer to an NSS lookup. Everything that is not
// ErrNSSNotFound, ErrNSSUnauthorized or ErrNSSRateLimited is "anything
// else": a bad request, a disabled server, a forbidden source address, a
// server error, or something that is not ssoosshd.
type NSSError struct {
	// StatusCode is the HTTP status.
	StatusCode int
	// ErrorCode is the envelope's error_code, one of apitypes'
	// ErrorCodeNSS* values from ssoosshd, empty when the body was not an
	// envelope.
	ErrorCode string
	// Message is the envelope's error message, empty when there was none.
	Message string
	// RetryAfter is the 429's Retry-After, zero when absent, unreadable or
	// already past.
	RetryAfter time.Duration
}

// Error implements error. The wording is ResponseError's, so a lookup that
// reaches the wrong server says so the same way every other call does.
func (e *NSSError) Error() string {
	msg := (&ResponseError{StatusCode: e.StatusCode, Message: e.Message}).Error()
	if e.ErrorCode == "" {
		return msg
	}
	return msg + " (" + e.ErrorCode + ")"
}

// Is classifies the failure for errors.Is.
func (e *NSSError) Is(target error) bool {
	switch target {
	case ErrNSSNotFound:
		return e.StatusCode == http.StatusNotFound && e.ErrorCode == apitypes.ErrorCodeNSSNotFound
	case ErrNSSUnauthorized:
		return e.StatusCode == http.StatusUnauthorized
	case ErrNSSRateLimited:
		return e.StatusCode == http.StatusTooManyRequests
	default:
		return false
	}
}

// GetNSSUserByName implements NSSClient.
func (c *HTTPClient) GetNSSUserByName(ctx context.Context, name string) (*apitypes.NSSUserResponse, error) {
	if err := checkNSSName(name); err != nil {
		return nil, err
	}
	return nssLookup[apitypes.NSSUserResponse](ctx, c, "/nss/user/name/"+url.PathEscape(name))
}

// GetNSSUserByUID implements NSSClient.
func (c *HTTPClient) GetNSSUserByUID(ctx context.Context, uid uint32) (*apitypes.NSSUserResponse, error) {
	return nssLookup[apitypes.NSSUserResponse](ctx, c, "/nss/user/uid/"+strconv.FormatUint(uint64(uid), 10))
}

// GetNSSGroupByName implements NSSClient.
func (c *HTTPClient) GetNSSGroupByName(ctx context.Context, name string) (*apitypes.NSSGroupResponse, error) {
	if err := checkNSSName(name); err != nil {
		return nil, err
	}
	return nssLookup[apitypes.NSSGroupResponse](ctx, c, "/nss/group/name/"+url.PathEscape(name))
}

// GetNSSGroupByGID implements NSSClient.
func (c *HTTPClient) GetNSSGroupByGID(ctx context.Context, gid uint32) (*apitypes.NSSGroupResponse, error) {
	return nssLookup[apitypes.NSSGroupResponse](ctx, c, "/nss/group/gid/"+strconv.FormatUint(uint64(gid), 10))
}

// checkNSSName refuses the names that cannot be put in a path segment
// without changing which route it names. Everything else is the server's
// to validate, which it does with the rules nss-systemd applies.
func checkNSSName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("%q is never a valid NSS name", name)
	}
	return nil
}

// nssLookup GETs path with the fleet key and decodes the envelope's data
// into a T. A function rather than a method because methods cannot take
// type parameters.
func nssLookup[T any](ctx context.Context, c *HTTPClient, path string) (*T, error) {
	if c.keyID == "" || c.keySecret == "" {
		return nil, ErrNSSNoKey
	}
	header := http.Header{}
	header.Set(apitypes.HeaderNSSKeyID, c.keyID)
	header.Set(apitypes.HeaderNSSKeySecret, c.keySecret)

	resp, err := c.send(ctx, http.MethodGet, path, nil, header)
	if err != nil {
		return nil, err
	}
	if resp.status >= 300 {
		return nil, decodeNSSError(resp, time.Now())
	}

	var env apitypes.Envelope[*T]
	if err := json.Unmarshal(resp.body, &env); err != nil {
		return nil, &undecodableError{contentType: resp.header.Get("Content-Type"), err: err}
	}
	if env.Data == nil {
		return nil, errors.New("the server answered the NSS lookup with no record")
	}
	return env.Data, nil
}

// decodeNSSError builds an *NSSError from a non-2xx answer. The envelope is
// read best-effort, as decodeResponseError does: a body that is not one
// still leaves the status to classify by.
func decodeNSSError(resp *response, now time.Time) *NSSError {
	nssErr := &NSSError{StatusCode: resp.status}
	var env apitypes.Envelope[json.RawMessage]
	if err := json.Unmarshal(resp.body, &env); err == nil {
		nssErr.Message = env.Error
		nssErr.ErrorCode = env.ErrorCode
	}
	if resp.status == http.StatusTooManyRequests {
		nssErr.RetryAfter = parseRetryAfter(resp.header.Get("Retry-After"), now)
	}
	return nssErr
}

// parseRetryAfter reads a Retry-After value in either form RFC 9110 allows,
// delta-seconds or an HTTP date, relative to now. Zero means absent,
// unreadable or already past, and the caller falls back on its own backoff.
func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	// 32 bits of seconds is 136 years and cannot overflow a Duration.
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}
