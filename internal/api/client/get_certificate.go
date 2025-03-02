// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"errors"
	"fmt"

	"github.com/mnestor/ssoossh/internal/api/types"
)

func (c *Client) GetCertificate(id string) (string, error) {
	res, err := c.
		SetResult(&types.CertificateRequestResponse{}).
		SetError(&types.ResponseError{}).
		Get(c.getApiPath("certificate"))
	if err != nil {
		e := res.Error().(*types.ResponseError)
		return "", errors.New(e.Message)
	}
	if res.IsSuccess() {
		certResponse := res.Result().(*types.CertificateRequestResponse)
		return certResponse.Certificate, nil
	} else {
		return "", fmt.Errorf("timeout waiting for response")
	}

}
