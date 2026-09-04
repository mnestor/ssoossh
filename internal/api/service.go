package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// RetrieveServiceCertificate implements Client.
func (c *HTTPClient) RetrieveServiceCertificate(ctx context.Context, code string) (string, error) {
	var result apitypes.Envelope[apitypes.RetrieveResponse]
	body := apitypes.RetrieveRequestBody{Code: code}
	if err := c.doJSON(ctx, http.MethodPost, "/certs/service/retrieve", body, &result); err != nil {
		return "", fmt.Errorf("failed to retrieve service certificate: %w", err)
	}

	return result.Data.Certificate, nil
}
