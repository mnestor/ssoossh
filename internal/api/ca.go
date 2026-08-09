package api

import (
	"context"
	"fmt"
)

// caResponse mirrors server/controller/ca.go's response shape.
type caResponse struct {
	CA string `json:"ca"`
}

// GetCA implements Client.
func (c *RestyClient) GetCA(ctx context.Context) (string, error) {
	var result caResponse
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

	return result.CA, nil
}
