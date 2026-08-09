package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"

	"resty.dev/v3"

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

	// RequestUserCertificate asks for an interactive user certificate for
	// publicKey (authorized_keys format). ssoosshd creates the pending
	// request and returns approvalURL immediately — the page the caller
	// should have the user open — then this call blocks (over a separate
	// SSE connection, per the SSE spec: POST-to-create is not itself a
	// stream) until a human resolves the request via the web UI
	// (approved/denied) or it expires.
	RequestUserCertificate(ctx context.Context, publicKey string, opts RequestedOptions) (approvalURL string, result *CertificateResult, err error)

	// RequestHostCertificate asks for first issuance of a host certificate
	// for hostname, gated by the OIDC approval chain, and blocks the same
	// way as RequestUserCertificate.
	RequestHostCertificate(ctx context.Context, publicKey, hostname string, opts RequestedOptions) (approvalURL string, result *CertificateResult, err error)

	// EnrollService asks to enroll publicKey for unattended service
	// certificate issuance and blocks the same way as
	// RequestUserCertificate. On approval, CertificateResult.Certificate is
	// not itself a usable certificate — it's empty, since approving a
	// service enrollment issues an enrollment code
	// (RetrieveServiceCertificate) rather than a certificate directly.
	EnrollService(ctx context.Context, publicKey string, opts RequestedOptions) (approvalURL string, result *CertificateResult, err error)

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

	serverURL := strings.TrimRight(cfg.ServerURL, "/")

	c := resty.New().
		SetBaseURL(serverURL+"/api").
		SetHeader("User-Agent", fmt.Sprintf("%s/%s (%s)", version.Name, version.Version, version.Github)).
		SetHeader("Accept", "application/json").
		SetHeader("Content-Type", "application/json").
		SetTLSClientConfig(tlsConfig).
		SetResultError(&errorBody{})

	return &RestyClient{http: c, serverURL: serverURL, tlsConfig: tlsConfig}, nil
}

func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.SkipVerifySSL,
	}

	return tlsConfig, nil
}
