package api

import (
	"context"
	"fmt"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// RetrieveServiceCertificate implements Client.
func (c *RestyClient) RetrieveServiceCertificate(ctx context.Context, code string) (string, error) {
	var result apitypes.RetrieveResponse
	resp, err := c.http.R().
		SetContext(ctx).
		SetBody(apitypes.RetrieveRequestBody{Code: code}).
		SetResult(&result).
		Post("/certs/service/retrieve")
	if err != nil {
		return "", fmt.Errorf("failed to retrieve service certificate: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return "", decodeResponseError(resp)
	}

	return result.Certificate, nil
}
