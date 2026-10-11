package api

import (
	"context"
	"net/http"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// RetrieveServiceCertificate implements Client.
func (c *HTTPClient) RetrieveServiceCertificate(ctx context.Context, code string) (string, error) {
	var result apitypes.Envelope[apitypes.RetrieveResponse]
	body := apitypes.RetrieveRequestBody{Code: code}
	// Unwrapped, as in GetCA: the caller names the action.
	if err := c.doJSON(ctx, http.MethodPost, "/certs/service/retrieve", body, &result); err != nil {
		return "", err
	}

	return result.Data.Certificate, nil
}
