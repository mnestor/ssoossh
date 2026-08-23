package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"resty.dev/v3"

	"github.com/mnestor/ssoossh/internal/tracelog"
	"github.com/mnestor/ssoossh/internal/version"
)

// Client is the set of ssoosshd API calls client and pam_ssoossh can make.
// RestyClient is the production implementation.
//
// Deliberately excludes host-certificate renewal and principal-mapping
// sync: both are gated server-side by HostCertAuthMiddleware, whose
// transport (custom header vs mTLS) isn't decided yet — see the TODO on
// server/middleware/host_cert_auth.go. Add those methods once it is,
// rather than guessing a scheme here.
type Client interface {
	// GetCA returns the CA's public key in authorized_keys format.
	GetCA(ctx context.Context) (string, error)

	// CreateUserRequest asks for an interactive user certificate for
	// publicKey (authorized_keys format). localUsername/localHostname are
	// the requesting client's own OS identity — for a user cert there is no
	// way to request one except via the local client, so this is who/where
	// the request actually came from (see
	// docs/dev/changes-next.md). It returns as soon as
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

// Config configures a RestyClient. Deliberately its own type rather than
// client/config.Config — internal/ packages can't import back up into
// client/ (see root CLAUDE.md) — so callers map their own config into
// this.
type Config struct {
	// ServerURL is the ssoosshd base URL, e.g. "https://sso.example.com".
	// Required.
	ServerURL string

	// SkipVerifySSL disables TLS certificate verification entirely.
	// Intended for development / self-signed setups. TLS verification is
	// otherwise limited to standard trust-chain and hostname checks — no
	// additional pinning.
	SkipVerifySSL bool

	// Logger receives per-request tracing. Nil means none, which is the
	// default and what pam_ssoossh relies on: it is loaded into sudo and
	// sshd, where writing to stdout or stderr corrupts the host process's
	// own output, and it routes its logging to syslog through its own
	// Logger rather than slog. Defaulting to slog.Default() here would put
	// this package's tracing on that stderr by accident, so the caller that
	// wants tracing has to say so.
	Logger *slog.Logger
}

// RestyClient is the production Client implementation, backed by resty.
type RestyClient struct {
	http *resty.Client

	// serverURL is cfg.ServerURL with no trailing slash and no "/api"
	// suffix (unlike http's base URL, which does have that suffix) — the
	// events_url ssoosshd returns from a create call already includes its
	// own "/api/..." path, so this is prepended directly to build the
	// events endpoint's absolute URL for resty's SSESource, which (unlike
	// http, a *resty.Client) has no base-URL concept of its own.
	serverURL string
	tlsConfig *tls.Config
}

var _ Client = (*RestyClient)(nil)

// NewClient builds a RestyClient talking to cfg.ServerURL.
func NewClient(cfg Config) (*RestyClient, error) {
	if cfg.ServerURL == "" {
		return nil, errors.New("server URL is required")
	}

	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	serverURL := normalizeServerURL(cfg.ServerURL)

	c := resty.New().
		SetBaseURL(serverURL+"/api").
		SetHeader("User-Agent", fmt.Sprintf("%s/%s (%s)", version.Name, version.Version, version.Github)).
		SetHeader("Accept", "application/json").
		SetHeader("Content-Type", "application/json").
		SetTLSClientConfig(tlsConfig).
		SetResultError(&errorBody{})

	installRequestTracing(c, cfg.Logger)

	return &RestyClient{http: c, serverURL: serverURL, tlsConfig: tlsConfig}, nil
}

// installRequestTracing logs every request and response to log, at levels
// internal/tracelog defines so the caller's -v count decides what is
// actually emitted. A nil logger installs nothing at all — see Config.Logger
// for why that is the default rather than slog.Default().
//
// Bodies and headers are logged only at the deepest level, and only after
// tracelog's redaction: this output exists to be pasted into bug reports.
func installRequestTracing(c *resty.Client, log *slog.Logger) {
	if log == nil {
		return
	}
	c.AddRequestMiddleware(func(_ *resty.Client, r *resty.Request) error {
		log.Debug("http request", "method", r.Method, "url", r.URL)
		if log.Enabled(r.Context(), tracelog.LevelTrace) {
			logHeaders(r.Context(), log, "http request header", r.Header)
			if body, ok := r.Body.(string); ok {
				log.Log(r.Context(), tracelog.LevelTrace, "http request body", "body", tracelog.Body(body))
			}
		}
		return nil
	})

	c.AddResponseMiddleware(func(_ *resty.Client, resp *resty.Response) error {
		log.Debug("http response",
			"method", resp.Request.Method,
			"url", resp.Request.URL,
			"status", resp.StatusCode(),
			"duration", resp.Duration().Round(time.Millisecond).String())
		if log.Enabled(resp.Request.Context(), tracelog.LevelTrace) {
			logHeaders(resp.Request.Context(), log, "http response header", resp.Header())
			log.Log(resp.Request.Context(), tracelog.LevelTrace, "http response body",
				"body", tracelog.Body(resp.String()))
		}
		return nil
	})
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
// produces requests resty cannot send and an approval URL no browser can
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
