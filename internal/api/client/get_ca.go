// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"errors"

	types "github.com/mnestor/ssoossh/internal/api/response_types"
)

func (c *Client) GetCA() (string, error) {
	res, err := c.
		SetResult(&types.ResponseCAList{}).
		SetError(&types.ResponseError{}).
		Get("ca")

	if err != nil {
		e := res.Error().(*types.ResponseError)
		if e.Message == "" {
			return "", errors.New("unable to talk to server please check your configuration")
		}
		return "", errors.New(e.Message)
	}

	list := res.Result().(*types.ResponseCAList)
	return list.CA, err
}
