// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"fmt"
	"strings"

	"github.com/mnestor/ssoossh/internal/version"
	"resty.dev/v3"
)

var (
	api_root = fmt.Sprintf("api/%s", version.ApiPath)
)

type Client struct {
	*resty.Request
	Server string
}

func (c *Client) getApiPath(p string) string {
	return fmt.Sprintf("%s/%s/%s", strings.Trim(c.Server, "/"), api_root, p)
}

func GetClient(s string) *Client {
	client := resty.New().
		// SetOutputDirectory("/workspace/log").
		// SetSaveResponse(true).
		SetHeader("Accept", "application/json").
		R()
		// SetDebug(true)

	return &Client{client, s}
}
