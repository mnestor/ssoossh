package api

import (
	"context"
	"fmt"
)

// userRequestBody / hostSignRequestBody / serviceEnrollRequestBody mirror
// server/controller/certrequests.go's request bodies.
type userRequestBody struct {
	PublicKey        string           `json:"public_key"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

type hostSignRequestBody struct {
	PublicKey        string           `json:"public_key"`
	Hostname         string           `json:"hostname"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

type serviceEnrollRequestBody struct {
	PublicKey        string           `json:"public_key"`
	RequestedOptions RequestedOptions `json:"requested_options,omitempty"`
}

// RequestUserCertificate implements Client.
func (c *RestyClient) RequestUserCertificate(ctx context.Context, publicKey string, opts RequestedOptions) (*CertificateResult, error) {
	return c.streamCertificateRequest(ctx, "/certs/user", userRequestBody{
		PublicKey:        publicKey,
		RequestedOptions: opts,
	})
}

// RequestHostCertificate implements Client.
func (c *RestyClient) RequestHostCertificate(ctx context.Context, publicKey, hostname string, opts RequestedOptions) (*CertificateResult, error) {
	return c.streamCertificateRequest(ctx, "/certs/host/sign", hostSignRequestBody{
		PublicKey:        publicKey,
		Hostname:         hostname,
		RequestedOptions: opts,
	})
}

// EnrollService implements Client.
func (c *RestyClient) EnrollService(ctx context.Context, publicKey string, opts RequestedOptions) (*CertificateResult, error) {
	return c.streamCertificateRequest(ctx, "/certs/service/enroll", serviceEnrollRequestBody{
		PublicKey:        publicKey,
		RequestedOptions: opts,
	})
}

// streamCertificateRequest POSTs body to path, then reads the resulting
// SSE stream for the request's outcome — ssoosshd holds that same
// connection open until the request resolves (approved/denied/expired)
// rather than exposing a separate poll-by-id endpoint (see
// server/controller/certrequests.go's streamOutcome), so there's nothing
// to do between the POST and reading the stream.
func (c *RestyClient) streamCertificateRequest(ctx context.Context, path string, body any) (*CertificateResult, error) {
	resp, err := c.http.R().
		SetContext(ctx).
		SetBody(body).
		SetResponseDoNotParse(true).
		Post(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode() >= 300 {
		return nil, decodeResponseError(resp)
	}

	return readCertificateEvent(resp.Body)
}
