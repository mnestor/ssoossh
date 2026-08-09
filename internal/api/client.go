package api

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"

	"resty.dev/v3"
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
	// publicKey (authorized_keys format) and blocks until a human resolves
	// it via the web UI (approved/denied) or the request expires.
	RequestUserCertificate(ctx context.Context, publicKey string, opts RequestedOptions) (*CertificateResult, error)

	// RequestHostCertificate asks for first issuance of a host certificate
	// for hostname, gated by the OIDC approval chain, and blocks the same
	// way as RequestUserCertificate.
	RequestHostCertificate(ctx context.Context, publicKey, hostname string, opts RequestedOptions) (*CertificateResult, error)

	// EnrollService asks to enroll publicKey for unattended service
	// certificate issuance and blocks the same way as
	// RequestUserCertificate. On approval, CertificateResult.Certificate is
	// not itself a usable certificate — it's empty, since approving a
	// service enrollment issues an enrollment code
	// (RetrieveServiceCertificate) rather than a certificate directly.
	EnrollService(ctx context.Context, publicKey string, opts RequestedOptions) (*CertificateResult, error)

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
	// Intended for development / self-signed setups that aren't also using
	// SSLFingerprint pinning.
	SkipVerifySSL bool
}

// RestyClient is the production Client implementation, backed by resty.
type RestyClient struct {
	http *resty.Client
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

	c := resty.New().
		SetBaseURL(strings.TrimRight(cfg.ServerURL, "/")+"/api").
		SetHeader("Accept", "application/json").
		SetHeader("Content-Type", "application/json").
		SetTLSClientConfig(tlsConfig)

	return &RestyClient{http: c}, nil
}

func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.SkipVerifySSL,
	}

	return tlsConfig, nil
}
