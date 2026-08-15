package api

import (
	"context"
	"fmt"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// GetCA implements Client.
func (c *RestyClient) GetCA(ctx context.Context) (string, error) {
	var result apitypes.Envelope[apitypes.CAResponse]
	resp, err := c.http.R().
		SetContext(ctx).
		SetResult(&result).
		Get("/ca")
	if err != nil {
		return "", fmt.Errorf("failed to get CA public key: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return "", decodeResponseError(resp)
	}

	return result.Data.CA, nil
}
