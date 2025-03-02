// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"errors"

	"github.com/mnestor/ssoossh/internal/api/types"
)

func (c *Client) GetCA() (string, error) {
	res, err := c.
		SetResult(&types.ResponseCAList{}).
		SetError(&types.ResponseError{}).
		Get(c.getApiPath("ca"))
	if err != nil {
		e := res.Error().(*types.ResponseError)
		return "", errors.New(e.Message)
	}

	list := res.Result().(*types.ResponseCAList)
	return list.CA, err
}
