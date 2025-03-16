// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"errors"

	types "github.com/mnestor/ssoossh/internal/api/response_types"
	"github.com/mnestor/ssoossh/internal/ssh"
)

func (c *Client) PostPubKey(kp ssh.KeyPairI) (string, error) {
	res, err := c.
		SetBody(types.SignRequest{
			PublicKey: kp.String(),
			Type:      kp.GetCertType(),
			Account:   kp.GetUsername(),
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
