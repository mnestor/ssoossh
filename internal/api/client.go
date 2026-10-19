package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/hostinfo"
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
	CreateUserRequest(ctx context.Context, hc hostinfo.HostContext, publicKey string, trustedCAFingerprints []string, opts RequestedOptions, policy *apitypes.ClientPolicy) (*PendingRequest, error)

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
	// otherwise the standard trust-chain and hostname checks against the
	// system trust store, unless TLSCAFile, TLSSystemRoots or TLSSPKIPins
	// pins it. It cannot be combined with any of them.
	SkipVerifySSL bool

	// TLSCAFile is a PEM bundle holding the only CAs a server certificate
	// may chain to. Setting it, TLSSystemRoots or TLSSPKIPins switches the
	// client to pinned mode: TLS 1.3 is the minimum and no proxy is used
	// whatever the environment says, so nothing outside the configuration
	// decides which server is reached. With TLSCAFile the system trust
	// store is never consulted, and SSL_CERT_FILE and SSL_CERT_DIR are
	// ignored. Empty, with neither of the others, keeps the system trust
	// store outside pinned mode. ssoosshd-nss sets it from its
	// tls_ca_file.
	TLSCAFile string

	// TLSSystemRoots is pinned mode with the host's system trust store as
	// the trust root: the chain and host name are verified against every
	// CA the host trusts, TLS 1.3 is the minimum and no proxy is used. On
	// Linux Go builds that store from the distribution's bundle, or from
	// SSL_CERT_FILE and SSL_CERT_DIR when the process environment sets
	// them. It cannot be combined with TLSCAFile or SkipVerifySSL; with
	// TLSSPKIPins both must hold. ssoosshd-nss sets it for
	// "tls_ca_file: system".
	TLSSystemRoots bool

	// TLSSPKIPins are base64 (standard encoding) SHA-256 digests of a
	// server leaf certificate's DER SubjectPublicKeyInfo, the leaf must
	// match one, and more than one lets a key rotate. With TLSCAFile or
	// TLSSystemRoots the chain and host name are verified as well; without
	// either the pin alone decides, as an SSH host key does. Setting any
	// switches the client to pinned mode (see TLSCAFile).
	TLSSPKIPins []string

	// systemRoots loads the pool TLSSystemRoots trusts. Nil means
	// x509.SystemCertPool; a test sets it so it never depends on the
	// store of the host it runs on.
	systemRoots func() (*x509.CertPool, error)

	// KeyID and KeySecret are a fleet key, sent as the X-Ssoossh-Key-Id and
	// X-Ssoossh-Key-Secret headers on the NSS lookups and on no other call.
	// The secret is redacted from tracing like an Authorization header.
	KeyID     string
	KeySecret string

	// RequestTimeout bounds each ordinary (non-streaming) call, from
	// connect to the last byte of the response. Zero means
	// DefaultRequestTimeout. The events stream is not affected: ssoosshd
	// sends no headers there until the request is decided.
	RequestTimeout time.Duration

	// DialTimeout bounds the TCP connect of each ordinary call, so an
	// unreachable server fails fast while a slow answer still gets all of
	// RequestTimeout. Zero keeps the standard library's 30 seconds. Like
	// RequestTimeout it does not apply to the events stream. ssoosshd-nss
	// sets it to 2 seconds.
	DialTimeout time.Duration

	// UserAgent replaces the User-Agent header this client sends, for a
	// program other than the ssoossh client (ssoosshd-nss sends
	// "ssoosshd-nss/<version>"). Empty keeps "ssoossh/<version> (<repo>)".
	UserAgent string

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

	// timeout is the http client's Timeout, kept to name it in the error
	// a timed-out call returns.
	timeout time.Duration

	// log is Config.Logger, nil when the caller asked for no tracing.
	log *slog.Logger

	// direct is set in pinned mode: every transport this client builds
	// ignores the proxy environment.
	direct bool

	// keyID and keySecret are Config.KeyID and Config.KeySecret, sent only
	// by the NSS lookups.
	keyID     string
	keySecret string
}

var _ Client = (*HTTPClient)(nil)

// DefaultRequestTimeout is Config.RequestTimeout when none is given. Long
// enough to cover a service retrieval's signing wait, which ssoosshd bounds
// at a tenth of its cert_options.client_timeout (30s by default).
const DefaultRequestTimeout = 2 * time.Minute

// NewClient builds an HTTPClient talking to cfg.ServerURL.
func NewClient(cfg Config) (*HTTPClient, error) {
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, ErrNoServerURL
	}
	if err := validateServerURL(cfg.ServerURL); err != nil {
		return nil, err
	}

	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}

	userAgent := cfg.UserAgent
	if userAgent == "" {
		userAgent = fmt.Sprintf("%s/%s (%s)", version.Name, version.Version, version.Github)
	}

	transport := newTransport(tlsConfig, cfg.pinned())
	if cfg.DialTimeout > 0 {
		// Same keep-alive as http.DefaultTransport's dialer; only the
		// connect bound changes.
		transport.DialContext = (&net.Dialer{Timeout: cfg.DialTimeout, KeepAlive: 30 * time.Second}).DialContext
	}
	transport.HTTP2 = healthChecks()

	return &HTTPClient{
		// A whole-request timeout is safe here because this client serves
		// only doJSON's ordinary calls. The events stream, which stays open
		// until a human decides, builds a client of its own (see sse.go).
		// Without it a server that accepts the connection and never answers
		// hangs the caller, which under ssh's Match exec means ssh itself.
		http:      &http.Client{Transport: transport, Timeout: timeout},
		timeout:   timeout,
		serverURL: normalizeServerURL(cfg.ServerURL),
		tlsConfig: tlsConfig,
		userAgent: userAgent,
		log:       cfg.Logger,
		direct:    cfg.pinned(),
		keyID:     cfg.KeyID,
		keySecret: cfg.KeySecret,
	}, nil
}

// newTransport clones http.DefaultTransport and installs tlsConfig on the
// copy. A clone rather than a bare &http.Transport{} so the defaults that
// matter in the field come along: ProxyFromEnvironment (an ssoosshd reached
// through a corporate proxy), the dial and idle-connection timeouts, and
// HTTP/2.
//
// direct drops the proxy: in pinned mode the configuration alone decides
// where a request goes, and HTTPS_PROXY is the environment deciding
// instead.
func newTransport(tlsConfig *tls.Config, direct bool) *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// not covered: http.DefaultTransport is a *http.Transport in every
		// Go release. This branch exists so that if one ever changes it,
		// the TLS configuration is still applied rather than silently
		// dropped, which for SkipVerifySSL would mean failing, but for a
		// pinned MinVersion would mean quietly negotiating a weaker one. A
		// bare transport has no Proxy, so direct holds here too.
		return &http.Transport{TLSClientConfig: tlsConfig}
	}
	transport := base.Clone()
	transport.TLSClientConfig = tlsConfig
	if direct {
		transport.Proxy = nil
	}
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

	resp, err := c.send(ctx, method, path, payload, nil)
	if err != nil {
		return err
	}
	if resp.status >= 300 {
		return decodeResponseError(resp.status, resp.body)
	}
	if err := json.Unmarshal(resp.body, out); err != nil {
		return &undecodableError{contentType: resp.header.Get("Content-Type"), err: err}
	}
	return nil
}

// response is one ordinary call's answer with its body read in full. The
// body is already closed, which is why this is not an *http.Response.
type response struct {
	status int
	header http.Header
	body   []byte
}

// send makes one ordinary call and reads the whole response, whatever its
// status. header carries request headers beyond the standard ones (the NSS
// lookups' fleet key); nil means none. They are traced like the rest,
// through tracelog's redaction.
func (c *HTTPClient) send(ctx context.Context, method, path string, payload []byte, header http.Header) (*response, error) {
	req, err := c.newRequest(ctx, method, c.apiURL(path), payload)
	if err != nil {
		return nil, err
	}
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}

	c.traceRequest(ctx, req, payload)

	// The bound a timeout here is reported against: the client's own, or
	// the caller's deadline when that is sooner (ssoosshd-nss gives each
	// attempt its own).
	timeout := c.timeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline).Round(time.Millisecond))
	}
	var conn connTracker
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), conn.trace()))

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, describeTransport(c.serverURL, timeout, conn.use(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read in full before deciding anything: the same bytes are both what
	// gets traced and what gets decoded, as either a result or an error
	// body.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", describeTransport(c.serverURL, timeout, conn.use(), err))
	}
	c.traceResponse(ctx, resp, body, time.Since(start))

	return &response{status: resp.StatusCode, header: resp.Header, body: body}, nil
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
// ErrNoServerURL is NewClient's error when Config.ServerURL is empty.
var ErrNoServerURL = errors.New("server URL is required")

// validateServerURL refuses a server address normalizeServerURL would turn
// into a request to somewhere else. It used to accept anything: "ftp://x"
// became https://ftp//x and failed with a DNS lookup of "ftp", and
// "https://" became a request to a host named "https".
func validateServerURL(raw string) error {
	s := strings.TrimSpace(raw)
	if scheme, rest, found := strings.Cut(s, "://"); found {
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("invalid server %q: the scheme must be http or https", raw)
		}
		if strings.Trim(rest, "/") == "" {
			return fmt.Errorf("invalid server %q: it names no host", raw)
		}
	}
	u, err := url.Parse(normalizeServerURL(s))
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid server %q: not a valid URL", raw)
	}
	return nil
}

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
