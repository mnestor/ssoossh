package api

import (
	"context"
	"fmt"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// RequestUserCertificate implements Client.
func (c *RestyClient) RequestUserCertificate(ctx context.Context, publicKey string, opts RequestedOptions) (string, *CertificateResult, error) {
	return c.createAndWait(ctx, "/certs/user", apitypes.UserRequestBody{
		PublicKey:        publicKey,
		RequestedOptions: opts,
	})
}

// RequestHostCertificate implements Client.
func (c *RestyClient) RequestHostCertificate(ctx context.Context, publicKey, hostname string, opts RequestedOptions) (string, *CertificateResult, error) {
	return c.createAndWait(ctx, "/certs/host/sign", apitypes.HostSignRequestBody{
		PublicKey:        publicKey,
		Hostname:         hostname,
		RequestedOptions: opts,
	})
}

// EnrollService implements Client.
func (c *RestyClient) EnrollService(ctx context.Context, publicKey string, opts RequestedOptions) (string, *CertificateResult, error) {
	return c.createAndWait(ctx, "/certs/service/enroll", apitypes.ServiceEnrollRequestBody{
		PublicKey:        publicKey,
		RequestedOptions: opts,
	})
}

// createAndWait POSTs body to path to create a pending certificate
// request, then opens a separate SSE connection (per the SSE spec — POST
// is not itself a stream) to wait for it to resolve. Returns the approval
// URL as soon as the create call succeeds, regardless of whether the wait
// that follows ultimately errors, since the caller may still want to show
// it (e.g. before reporting a later timeout).
func (c *RestyClient) createAndWait(ctx context.Context, path string, body any) (string, *CertificateResult, error) {
	var created apitypes.CreateRequestResponse
	resp, err := c.http.R().
		SetContext(ctx).
		SetBody(body).
		SetResult(&created).
		Post(path)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create certificate request: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return "", nil, decodeResponseError(resp)
	}

	result, err := waitForOutcome(ctx, c.tlsConfig, c.serverURL+created.EventsURL)
	return created.ApprovalURL, result, err
}
