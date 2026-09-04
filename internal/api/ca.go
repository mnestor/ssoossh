package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// GetCA implements Client.
func (c *HTTPClient) GetCA(ctx context.Context) (string, error) {
	var result apitypes.Envelope[apitypes.CAResponse]
	if err := c.doJSON(ctx, http.MethodGet, "/ca", nil, &result); err != nil {
		return "", fmt.Errorf("failed to get CA public key: %w", err)
	}

	return result.Data.CA, nil
}
