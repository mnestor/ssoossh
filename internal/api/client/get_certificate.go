// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"errors"

	types "github.com/mnestor/ssoossh/internal/api/response_types"
)

func (c *Client) GetCertificate(id string) (string, error) {
	result := &types.CertificateRequestResponse{}
	resErr := &types.ResponseError{}
	res, err := c.
		SetResult(result).
		SetError(resErr).
		Get("certificate")
	if err != nil {
		return "", err
	}

	if resErr.StatusText != "success" && resErr.StatusText != "" {
		return "", errors.New(resErr.Message)
	}

	if res.IsSuccess() && result.StatusText == "success" {
		return result.Certificate, nil
	} else {
		return "", errors.New(result.Message)
	}

}
