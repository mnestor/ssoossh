// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"errors"

	"github.com/mnestor/ssoossh/internal/api/types"
	"github.com/mnestor/ssoossh/internal/ssh"
)

func (c *Client) PostPubKey(kp *ssh.KeyPair) (string, error) {
	res, err := c.
		SetBody(types.SignRequest{
			PublicKey: kp.String(),
		}).
		SetResult(&types.SignRequestResponse{}).
		SetError(&types.ResponseError{}).
		Post(c.getApiPath("signreq"))
	if err != nil {
		e := res.Error().(*types.ResponseError)
		return "", errors.New(e.Message)
	}

	reqid := res.Result().(*types.SignRequestResponse)
	return reqid.ID, err
}
