// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"errors"

	types "github.com/mnestor/ssoossh/internal/api/response_types"
)

func (c *Client) PostPubKey(pkey string, certType string, account string) (string, error) {
	res, err := c.
		SetBody(types.SignRequest{
			PublicKey: pkey,
			Type:      certType,
			Account:   account,
		}).
		SetResult(&types.SignRequestResponse{}).
		SetError(&types.ResponseError{}).
		Post("signreq")

	if err != nil {
		e := res.Error().(*types.ResponseError)
		return "", errors.New(e.Message)
	}

	reqid := res.Result().(*types.SignRequestResponse)
	return reqid.ID, err
}
