package api

import (
	"context"
	"fmt"
)

// retrieveRequestBody / retrieveResponse mirror
// server/controller/enrollment.go's request/response shapes.
type retrieveRequestBody struct {
	Code string `json:"code"`
}

type retrieveResponse struct {
	Certificate string `json:"certificate"`
}

// RetrieveServiceCertificate implements Client.
func (c *RestyClient) RetrieveServiceCertificate(ctx context.Context, code string) (string, error) {
	var result retrieveResponse
	resp, err := c.http.R().
		SetContext(ctx).
		SetBody(retrieveRequestBody{Code: code}).
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
