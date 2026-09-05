package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mnestor/ssoossh/internal/tracelog"
	"github.com/mnestor/ssoossh/internal/version"
)

// Client is the set of ssoosshd API calls an API consumer can make.
// HTTPClient is the production implementation.
//
// There is no host-certificate or principal-mapping-sync call here: ssoosshd
// issues no host identity
// (https://mnestor.github.io/ssoossh/project/decisions/), and principal
// mapping is purely local to the client (`ssoossh host mapping`), never
// synced from the server.
type Client interface {
	// GetCA returns the CA's public key in authorized_keys format.
	GetCA(ctx context.Context) (string, error)

	// CreateUserRequest asks for an interactive user certificate for
	// publicKey (authorized_keys format). localUsername/localHostname are
	// the requesting client's own OS identity — for a user cert there is no
	// way to request one except via the local client, so this is who/where
	// the request actually came from. It returns as soon as
	// ssoosshd has created the request, so the caller can show
	// PendingRequest.ApprovalURL to a human before blocking on
	// AwaitCertificate.
	CreateUserRequest(ctx context.Context, publicKey, localUsername, localHostname string, opts RequestedOptions) (*PendingRequest, error)

	// CreateServiceEnrollment asks to enroll publicKey for unattended
	// service certificate issuance. Returns without waiting, like
	// CreateUserRequest. Note that approving one of these yields
	// CertificateResult.Code (an enrollment token to redeem later via
	// RetrieveServiceCertificate) rather than a certificate.
	CreateServiceEnrollment(ctx context.Context, publicKey string, opts RequestedOptions) (*PendingRequest, error)

	// CreatePAMRequest asks for a short-lived PAM certificate authenticating
	// username (the local account being authenticated, e.g. by `sudo` —
	// distinct from whatever identity approves in the browser). Returns
	// without waiting, like CreateUserRequest.
	CreatePAMRequest(ctx context.Context, publicKey, username string, opts RequestedOptions) (*PendingRequest, error)

	// AwaitCertificate blocks until req is resolved — approved, denied,
	// expired, enrolled, or failed — over a separate SSE connection, per the
	// SSE spec: the POST that created the request is not itself a stream.
	// req must have come from one of the Create* calls on this same client.
	//
	// Safe to call again after a failure: ssoosshd's wait is idempotent per
	// request, so a fresh connection picks up wherever the request actually
	// is.
	AwaitCertificate(ctx context.Context, req *PendingRequest) (*CertificateResult, error)

	// RetrieveServiceCertificate redeems an enrollment code (from a
	// previously approved EnrollService call) for a signed service
	// certificate in authorized_keys format.
	RetrieveServiceCertificate(ctx context.Context, code string) (string, error)
}

// Config configures an HTTPClient. Deliberately its own type rather than
// client/config.Config — internal/ packages can't import back up into client/
// (see https://mnestor.github.io/ssoossh/internals/invariants/) — so callers
// map their own config into this.
type Config struct {
	// ServerURL is the ssoosshd base URL, e.g. "https://sso.example.com".
	// Required.
	ServerURL string

	// SkipVerifySSL disables TLS certificate verification entirely.
	// Intended for development / self-signed setups. TLS verification is
	// otherwise limited to standard trust-chain and hostname checks — no
	// additional pinning.
	SkipVerifySSL bool

	// Logger receives per-request tracing. Nil means none, and the caller
	// that wants tracing has to say so: defaulting to slog.Default() would
	// put this package's tracing on the host process's stderr by accident,
	// which is not a library's decision to make.
	//
	// The rule arrived when a Go PAM module linked this package into sudo,
	// where a stray write to stderr corrupts the host process's own output.
	// That module is C now and links none of this, so the constraint is no
	// longer load-bearing — but the design was right on its own terms and
	// reverting it would surprise every caller that relies on the silence.
	Logger *slog.Logger
}

// HTTPClient is the production Client implementation, built on net/http
// directly.
//
// Directly, rather than on an HTTP convenience library. The three calls and
// one event stream below used to go through resty.dev/v3, which dragged in
// encoding/xml and regexp for features none of them use; removing it took
// 726 KB off the binary. That mattered acutely when a Go PAM module linked
// this package into a c-shared object mapped into every sudo, which is no
// longer the case — the module is C and links none of this. The dependency
// is still not worth re-adding for four calls.
type HTTPClient struct {
	http *http.Client

	// serverURL is cfg.ServerURL with no trailing slash and no "/api"
	// suffix — the events_url and approval_url ssoosshd returns from a
	// create call already include their own "/api/..." path, so this is
	// prepended directly to build their absolute URLs. Ordinary calls go
	// through apiURL, which adds the "/api" prefix itself.
	serverURL string

	// tlsConfig is held as well as being installed on http's transport,
	// because the events stream builds a client of its own (see sse.go).
	tlsConfig *tls.Config

	userAgent string

	// log is Config.Logger, nil when the caller asked for no tracing.
	log *slog.Logger
}

var _ Client = (*HTTPClient)(nil)

// NewClient builds an HTTPClient talking to cfg.ServerURL.
func NewClient(cfg Config) (*HTTPClient, error) {
	if cfg.ServerURL == "" {
		return nil, errors.New("server URL is required")
	}

	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		// not covered: buildTLSConfig cannot fail today — it assembles a
		// tls.Config from two fields and reads nothing from disk. The error
		// is in its signature for the trust-store options that would.
		return nil, err
	}

	return &HTTPClient{
		// No Client.Timeout: it would apply to the whole request including
		// the body, which is wrong for an events stream that legitimately
		// stays open until a human approves. Every call here takes a
		// context, and that is what bounds it.
		http:      &http.Client{Transport: newTransport(tlsConfig)},
		serverURL: normalizeServerURL(cfg.ServerURL),
		tlsConfig: tlsConfig,
		userAgent: fmt.Sprintf("%s/%s (%s)", version.Name, version.Version, version.Github),
		log:       cfg.Logger,
	}, nil
}

// newTransport clones http.DefaultTransport and installs tlsConfig on the
// copy. A clone rather than a bare &http.Transport{} so the defaults that
// matter in the field come along: ProxyFromEnvironment (an ssoosshd reached
// through a corporate proxy), the dial and idle-connection timeouts, and
// HTTP/2.
func newTransport(tlsConfig *tls.Config) *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// not covered: http.DefaultTransport is a *http.Transport in every
		// Go release. This branch exists so that if one ever changes it,
		// the TLS configuration is still applied rather than silently
		// dropped — which for SkipVerifySSL would mean failing, but for a
		// pinned MinVersion would mean quietly negotiating a weaker one.
		return &http.Transport{TLSClientConfig: tlsConfig}
	}
	transport := base.Clone()
	transport.TLSClientConfig = tlsConfig
	return transport
}

// apiURL builds the absolute URL for an API path, which is always relative
// to the server's "/api" prefix.
func (c *HTTPClient) apiURL(path string) string {
	return c.serverURL + "/api" + path
}

// doJSON makes one ordinary (non-streaming) API call: it sends body as JSON
// if there is one, and decodes the response into out, which every call here
// has. A non-2xx status is returned as a *ResponseError so every caller
// branches on the same type rather than on a status code it read itself.
func (c *HTTPClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			// not covered: every body passed here is a struct of strings
			// and slices from internal/apitypes, none of which can fail to
			// marshal.
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	req, err := c.newRequest(ctx, method, c.apiURL(path), payload)
	if err != nil {
		return err
	}

	c.traceRequest(ctx, req, payload)

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Read in full before deciding anything: the same bytes are both what
	// gets traced and what gets decoded, as either a result or an error
	// body.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	c.traceResponse(ctx, resp, respBody, time.Since(start))

	if resp.StatusCode >= 300 {
		return decodeResponseError(resp.StatusCode, respBody)
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}

// newRequest builds a request carrying this client's standard headers.
// Content-Type is set only when there is a body to describe.
func (c *HTTPClient) newRequest(ctx context.Context, method, url string, payload []byte) (*http.Request, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", method, url, err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// traceRequest logs an outgoing request at the levels internal/tracelog
// defines, so the caller's -v count decides what is actually emitted. A nil
// logger emits nothing at all — see Config.Logger for why that is the
// default rather than slog.Default().
//
// Bodies and headers are logged only at the deepest level, and only after
// tracelog's redaction: this output exists to be pasted into bug reports.
func (c *HTTPClient) traceRequest(ctx context.Context, req *http.Request, payload []byte) {
	if c.log == nil {
		return
	}
	c.log.Debug("http request", "method", req.Method, "url", req.URL.String())
	if !c.log.Enabled(ctx, tracelog.LevelTrace) {
		return
	}
	logHeaders(ctx, c.log, "http request header", req.Header)
	if len(payload) > 0 {
		c.log.Log(ctx, tracelog.LevelTrace, "http request body", "body", tracelog.Body(string(payload)))
	}
}

// traceResponse is traceRequest's other half, including how long the call
// took.
func (c *HTTPClient) traceResponse(ctx context.Context, resp *http.Response, body []byte, elapsed time.Duration) {
	if c.log == nil {
		return
	}
	c.log.Debug("http response",
		"method", resp.Request.Method,
		"url", resp.Request.URL.String(),
		"status", resp.StatusCode,
		"duration", elapsed.Round(time.Millisecond).String())
	if !c.log.Enabled(ctx, tracelog.LevelTrace) {
		return
	}
	logHeaders(ctx, c.log, "http response header", resp.Header)
	c.log.Log(ctx, tracelog.LevelTrace, "http response body", "body", tracelog.Body(string(body)))
}

// logHeaders emits one line per header, redacted. One line each rather than
// a single joined value so a long header set stays readable in a terminal.
func logHeaders(ctx context.Context, log *slog.Logger, msg string, headers http.Header) {
	for name, values := range headers {
		for _, v := range values {
			log.Log(ctx, tracelog.LevelTrace, msg, "name", name, "value", tracelog.Header(name, v))
		}
	}
}

// normalizeServerURL trims any trailing slash and supplies the scheme when
// the configured value has none, which is what the --server flag has always
// promised ("assumes https if omitted"). Without this a bare "example.com"
// produces requests that cannot be sent and an approval URL no browser can
// open.
func normalizeServerURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	return "https://" + trimmed
}

func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.SkipVerifySSL,
		MinVersion:         tls.VersionTLS13,
	}

	return tlsConfig, nil
}
