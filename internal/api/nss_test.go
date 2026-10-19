package api

// Test methodology: an httptest TLS server stands in for ssoosshd's
// /api/nss/* routes and records what each request carried; the client is
// pinned to that server's key, the way ssoossh-nss will run it. Error
// classes are driven by the status, error code and Retry-After the server
// returns.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/tracelog"
)

// Fleet key test values. The secret is distinctive so a test can search a
// trace for it.
const (
	testKeyID     = "nss_k7m4qp2x"
	testKeySecret = "ssoossh_nss_c2VjcmV0LXRoYXQtbXVzdC1uZXZlci1iZS1sb2dnZWQ"
)

// nssRequest is what the fake server saw of one request.
type nssRequest struct {
	path, rawPath, keyID, keySecret string
}

// nssServer answers every request with status, header and body, and
// records each request. It is pinned, so it doubles as the TLS fixture.
type nssServer struct {
	*httptest.Server

	mu   sync.Mutex
	seen []nssRequest
}

// newNSSServer starts a TLS server answering with status, extra headers
// and body.
func newNSSServer(t *testing.T, status int, header map[string]string, body string) *nssServer {
	t.Helper()
	s := &nssServer{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, nssRequest{
			path:      r.URL.Path,
			rawPath:   r.URL.EscapedPath(),
			keyID:     r.Header.Get(apitypes.HeaderNSSKeyID),
			keySecret: r.Header.Get(apitypes.HeaderNSSKeySecret),
		})
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

// requests returns what the server has seen so far.
func (s *nssServer) requests() []nssRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]nssRequest(nil), s.seen...)
}

// nssClient builds a client pinned to s and carrying the test fleet key.
func nssClient(t *testing.T, s *nssServer, mutate func(*Config)) *HTTPClient {
	t.Helper()
	cfg := Config{
		ServerURL:   s.URL,
		TLSSPKIPins: []string{pinOf(s.Certificate())},
		KeyID:       testKeyID,
		KeySecret:   testKeySecret,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return c
}

const (
	userBody  = `{"data":{"name":"Mike.N","uid":1234,"gid":1234,"groups":[{"gid":1234,"name":"Mike.N"},{"gid":27,"name":"sudo"}],"home":"/home/mike","shell":"/bin/bash","gecos":"Mike Nestor","status":"active","key_id":"nss_k7m4qp2x"}}`
	groupBody = `{"data":{"name":"sudo","gid":27,"members":["Mike.N","alice"],"members_truncated":false}}`
)

// strPtr returns a pointer to s, for the nullable fields.
func strPtr(s string) *string { return &s }

// TestNSSLookups_ShouldRequestTheRouteAndDecodeTheRecord covers each
// lookup's path and decoding.
func TestNSSLookups_ShouldRequestTheRouteAndDecodeTheRecord(t *testing.T) {
	t.Parallel()

	wantUser := &apitypes.NSSUserResponse{
		Name: "Mike.N", UID: 1234, GID: 1234,
		Groups: []apitypes.NSSMembership{{GID: 1234, Name: "Mike.N"}, {GID: 27, Name: "sudo"}},
		Home:   strPtr("/home/mike"), Shell: strPtr("/bin/bash"),
		Gecos: "Mike Nestor", Status: apitypes.NSSStatusActive, KeyID: testKeyID,
	}
	wantGroup := &apitypes.NSSGroupResponse{Name: "sudo", GID: 27, Members: []string{"Mike.N", "alice"}}

	tests := []struct {
		name     string
		body     string
		call     func(*HTTPClient) (any, error)
		wantPath string
		wantRaw  string
		want     any
	}{
		{
			name:     "should send the name verbatim when looking a user up by name",
			body:     userBody,
			call:     func(c *HTTPClient) (any, error) { return c.GetNSSUserByName(context.Background(), "Mike.N") },
			wantPath: "/api/nss/user/name/Mike.N",
			want:     wantUser,
		},
		{
			name:     "should escape the name when it holds characters a path cannot",
			body:     userBody,
			call:     func(c *HTTPClient) (any, error) { return c.GetNSSUserByName(context.Background(), "a b?c#d%e") },
			wantPath: "/api/nss/user/name/a b?c#d%e",
			wantRaw:  "/api/nss/user/name/a%20b%3Fc%23d%25e",
			want:     wantUser,
		},
		{
			name:     "should send the uid in decimal when looking a user up by uid",
			body:     userBody,
			call:     func(c *HTTPClient) (any, error) { return c.GetNSSUserByUID(context.Background(), 4294967295) },
			wantPath: "/api/nss/user/uid/4294967295",
			want:     wantUser,
		},
		{
			name:     "should send the name when looking a group up by name",
			body:     groupBody,
			call:     func(c *HTTPClient) (any, error) { return c.GetNSSGroupByName(context.Background(), "sudo") },
			wantPath: "/api/nss/group/name/sudo",
			want:     wantGroup,
		},
		{
			name:     "should send the gid when looking a group up by gid",
			body:     groupBody,
			call:     func(c *HTTPClient) (any, error) { return c.GetNSSGroupByGID(context.Background(), 27) },
			wantPath: "/api/nss/group/gid/27",
			want:     wantGroup,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newNSSServer(t, http.StatusOK, nil, tt.body)
			got, err := tt.call(nssClient(t, s, nil))
			if err != nil {
				t.Fatalf("lookup error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}

			seen := s.requests()
			if len(seen) != 1 {
				t.Fatalf("server saw %d requests, want 1", len(seen))
			}
			if seen[0].path != tt.wantPath {
				t.Errorf("path = %q, want %q", seen[0].path, tt.wantPath)
			}
			if tt.wantRaw != "" && seen[0].rawPath != tt.wantRaw {
				t.Errorf("escaped path = %q, want %q", seen[0].rawPath, tt.wantRaw)
			}
		})
	}
}

// TestNSSLookups_ShouldSendTheFleetKey checks both headers arrive intact.
func TestNSSLookups_ShouldSendTheFleetKey(t *testing.T) {
	t.Parallel()

	s := newNSSServer(t, http.StatusOK, nil, userBody)
	if _, err := nssClient(t, s, nil).GetNSSUserByUID(context.Background(), 1234); err != nil {
		t.Fatalf("lookup error = %v", err)
	}

	got := s.requests()[0]
	if got.keyID != testKeyID || got.keySecret != testKeySecret {
		t.Errorf("server saw key %q / %q, want %q / %q", got.keyID, got.keySecret, testKeyID, testKeySecret)
	}
}

// TestNSSLookups_ShouldNotSendTheFleetKeyOnOtherCalls covers the rule that
// the key goes to the NSS routes and nowhere else.
func TestNSSLookups_ShouldNotSendTheFleetKeyOnOtherCalls(t *testing.T) {
	t.Parallel()

	s := newNSSServer(t, http.StatusOK, nil, caResponseBody)
	if _, err := nssClient(t, s, nil).GetCA(context.Background()); err != nil {
		t.Fatalf("GetCA() error = %v", err)
	}

	if got := s.requests()[0]; got.keyID != "" || got.keySecret != "" {
		t.Errorf("GetCA sent key headers %q / %q", got.keyID, got.keySecret)
	}
}

// TestNSSLookups_ShouldRefuseBeforeSending covers the lookups that fail
// without a request: no key, or a name that would change the route.
func TestNSSLookups_ShouldRefuseBeforeSending(t *testing.T) {
	t.Parallel()

	noKey := func(cfg *Config) { cfg.KeySecret = "" }

	tests := []struct {
		name    string
		mutate  func(*Config)
		call    func(*HTTPClient) error
		wantErr error
	}{
		{
			name:    "should refuse a lookup when the client has no fleet key",
			mutate:  noKey,
			call:    func(c *HTTPClient) error { _, err := c.GetNSSUserByUID(context.Background(), 1); return err },
			wantErr: ErrNSSNoKey,
		},
		{
			name: "should refuse a user name when it is empty",
			call: func(c *HTTPClient) error { _, err := c.GetNSSUserByName(context.Background(), ""); return err },
		},
		{
			name: "should refuse a user name when it is a dot",
			call: func(c *HTTPClient) error { _, err := c.GetNSSUserByName(context.Background(), "."); return err },
		},
		{
			name: "should refuse a group name when it is two dots",
			call: func(c *HTTPClient) error { _, err := c.GetNSSGroupByName(context.Background(), ".."); return err },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newNSSServer(t, http.StatusOK, nil, userBody)
			err := tt.call(nssClient(t, s, tt.mutate))
			if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
				t.Errorf("got %v, want an error (%v)", err, tt.wantErr)
			}
			if n := len(s.requests()); n != 0 {
				t.Errorf("server saw %d requests, want none", n)
			}
		})
	}
}

// TestNSSLookups_ErrorClasses covers how each failed answer is classified.
func TestNSSLookups_ErrorClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		status         int
		header         map[string]string
		body           string
		wantNotFound   bool
		wantUnauth     bool
		wantRateLimit  bool
		wantCode       string
		wantRetryAfter time.Duration
	}{
		{
			name:         "should report not found when the server answers nss_not_found",
			status:       http.StatusNotFound,
			body:         `{"data":null,"error":"no such user","error_code":"nss_not_found"}`,
			wantNotFound: true,
			wantCode:     apitypes.ErrorCodeNSSNotFound,
		},
		{
			name:     "should not report not found when NSS is disabled on the server",
			status:   http.StatusNotFound,
			body:     `{"data":null,"error":"nss is disabled","error_code":"nss_disabled"}`,
			wantCode: apitypes.ErrorCodeNSSDisabled,
		},
		{
			name:   "should not report not found when the 404 is not from ssoosshd",
			status: http.StatusNotFound,
			body:   `<html>404 page not found</html>`,
		},
		{
			name:       "should report unauthorized when the key is refused",
			status:     http.StatusUnauthorized,
			body:       `{"data":null,"error":"unauthorized","error_code":"nss_unauthorized"}`,
			wantUnauth: true,
			wantCode:   apitypes.ErrorCodeNSSUnauthorized,
		},
		{
			name:           "should report rate limiting with its Retry-After when the server sends one",
			status:         http.StatusTooManyRequests,
			header:         map[string]string{"Retry-After": "7"},
			body:           `{"data":null,"error":"slow down","error_code":"nss_rate_limited"}`,
			wantRateLimit:  true,
			wantCode:       apitypes.ErrorCodeNSSRateLimited,
			wantRetryAfter: 7 * time.Second,
		},
		{
			name:          "should report rate limiting with no Retry-After when the server sends none",
			status:        http.StatusTooManyRequests,
			body:          `{"data":null,"error":"slow down","error_code":"nss_rate_limited"}`,
			wantRateLimit: true,
			wantCode:      apitypes.ErrorCodeNSSRateLimited,
		},
		{
			name:     "should report anything else when the request was bad",
			status:   http.StatusBadRequest,
			body:     `{"data":null,"error":"invalid name","error_code":"nss_bad_request"}`,
			wantCode: apitypes.ErrorCodeNSSBadRequest,
		},
		{
			name:     "should report anything else when the source address is forbidden",
			status:   http.StatusForbidden,
			body:     `{"data":null,"error":"forbidden","error_code":"nss_forbidden"}`,
			wantCode: apitypes.ErrorCodeNSSForbidden,
		},
		{
			name:   "should report anything else when the server fails",
			status: http.StatusInternalServerError,
			body:   ``,
		},
		{
			name:   "should ignore Retry-After when the status is not 429",
			status: http.StatusServiceUnavailable,
			header: map[string]string{"Retry-After": "7"},
			body:   ``,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newNSSServer(t, tt.status, tt.header, tt.body)
			_, err := nssClient(t, s, nil).GetNSSUserByName(context.Background(), "mike")

			var nssErr *NSSError
			if !errors.As(err, &nssErr) {
				t.Fatalf("got %v, want an *NSSError", err)
			}
			if nssErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", nssErr.StatusCode, tt.status)
			}
			if nssErr.ErrorCode != tt.wantCode {
				t.Errorf("ErrorCode = %q, want %q", nssErr.ErrorCode, tt.wantCode)
			}
			if nssErr.RetryAfter != tt.wantRetryAfter {
				t.Errorf("RetryAfter = %s, want %s", nssErr.RetryAfter, tt.wantRetryAfter)
			}
			if got := errors.Is(err, ErrNSSNotFound); got != tt.wantNotFound {
				t.Errorf("errors.Is(ErrNSSNotFound) = %v, want %v", got, tt.wantNotFound)
			}
			if got := errors.Is(err, ErrNSSUnauthorized); got != tt.wantUnauth {
				t.Errorf("errors.Is(ErrNSSUnauthorized) = %v, want %v", got, tt.wantUnauth)
			}
			if got := errors.Is(err, ErrNSSRateLimited); got != tt.wantRateLimit {
				t.Errorf("errors.Is(ErrNSSRateLimited) = %v, want %v", got, tt.wantRateLimit)
			}
			if errors.Is(err, ErrNSSNoKey) {
				t.Error("a server answer classified as a missing key")
			}
		})
	}
}

// TestNSSLookups_ShouldRefuseAnUnusableSuccess covers 2xx answers that do
// not carry a record.
func TestNSSLookups_ShouldRefuseAnUnusableSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "should refuse a success when it carries no record", body: `{"data":null}`, want: "no record"},
		{name: "should refuse a success when it is not JSON", body: `<html>captive portal</html>`, want: "malformed JSON"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newNSSServer(t, http.StatusOK, nil, tt.body)
			_, err := nssClient(t, s, nil).GetNSSGroupByGID(context.Background(), 27)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error mentioning %q", err, tt.want)
			}
		})
	}
}

// TestNSSLookups_ShouldReturnATransportErrorWhenTheServerIsGone covers a
// lookup that never gets an answer.
func TestNSSLookups_ShouldReturnATransportErrorWhenTheServerIsGone(t *testing.T) {
	t.Parallel()

	s := newNSSServer(t, http.StatusOK, nil, userBody)
	c := nssClient(t, s, nil)
	s.Close()

	_, err := c.GetNSSUserByName(context.Background(), "mike")
	var nssErr *NSSError
	if err == nil || errors.As(err, &nssErr) {
		t.Errorf("got %v, want a transport error rather than a server answer", err)
	}
}

// TestNSSLookups_ShouldRedactTheKeySecretFromTraces is the rule the
// proposal states for every log: the key id may appear, the secret never.
func TestNSSLookups_ShouldRedactTheKeySecretFromTraces(t *testing.T) {
	t.Parallel()

	s := newNSSServer(t, http.StatusUnauthorized, nil, `{"data":null,"error":"unauthorized","error_code":"nss_unauthorized"}`)
	var captured bytes.Buffer
	c := nssClient(t, s, func(cfg *Config) { cfg.Logger = traceLogger(&captured) })

	_, err := c.GetNSSUserByName(context.Background(), "mike")
	if err == nil {
		t.Fatal("expected the 401 to be an error")
	}

	trace := captured.String()
	if strings.Contains(trace, testKeySecret) {
		t.Errorf("the key secret reached the trace:\n%s", trace)
	}
	if strings.Contains(err.Error(), testKeySecret) {
		t.Errorf("the key secret reached the error: %v", err)
	}
	if !strings.Contains(trace, testKeyID) {
		t.Errorf("the key id is missing from the trace, which needs it for correlation:\n%s", trace)
	}
	if !strings.Contains(trace, tracelog.Redacted) {
		t.Errorf("no redaction marker in the trace:\n%s", trace)
	}
}

// TestNSSError_Error pins the message for an envelope and for a bare
// status.
func TestNSSError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *NSSError
		want string
	}{
		{
			name: "should name the code when the server sent one",
			err:  &NSSError{StatusCode: http.StatusBadRequest, ErrorCode: apitypes.ErrorCodeNSSBadRequest, Message: "invalid name"},
			want: "ssoosshd returned status 400: invalid name (nss_bad_request)",
		},
		{
			name: "should suspect the server address when a 404 has no envelope",
			err:  &NSSError{StatusCode: http.StatusNotFound},
			want: "the server answered 404 Not Found; check that the server address points at ssoosshd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseRetryAfter covers both forms RFC 9110 allows and what is not
// either.
func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "should give zero when the header is absent", value: "", want: 0},
		{name: "should read delta-seconds", value: "7", want: 7 * time.Second},
		{name: "should read delta-seconds when padded", value: " 30 ", want: 30 * time.Second},
		{name: "should read zero seconds as zero", value: "0", want: 0},
		{name: "should read the largest 32-bit delay", value: "4294967295", want: 4294967295 * time.Second},
		{name: "should give zero when the delay overflows 32 bits", value: "4294967296", want: 0},
		{name: "should give zero when the delay is negative", value: "-5", want: 0},
		{name: "should give zero when the value is not a delay", value: "soon", want: 0},
		{name: "should read an HTTP date in the future", value: now.Add(90 * time.Second).Format(http.TimeFormat), want: 90 * time.Second},
		{name: "should give zero when the HTTP date has passed", value: now.Add(-time.Minute).Format(http.TimeFormat), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseRetryAfter(tt.value, now); got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}

// TestSend_ShouldReportARequestItCannotBuild covers a server URL that
// NewClient would have refused, reached by building the client directly.
func TestSend_ShouldReportARequestItCannotBuild(t *testing.T) {
	t.Parallel()

	c := &HTTPClient{serverURL: "http://exa\x7fmple.com", http: &http.Client{}, keyID: testKeyID, keySecret: testKeySecret}
	_, err := c.GetNSSUserByUID(context.Background(), 1234)
	if err == nil || !strings.Contains(err.Error(), "build GET") {
		t.Errorf("got %v, want the request-building error", err)
	}
}
