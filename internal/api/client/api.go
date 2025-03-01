// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"fmt"
	"io"
	"strings"

	"github.com/mnestor/ssoossh/internal/version"
	"resty.dev/v3"
)

var (
	outWriter io.Writer
	errWriter io.Writer
	api_root  = fmt.Sprintf("api/%s", version.ApiPath)
)

type Client struct {
	*resty.Request
	Server string
}

func (c *Client) getApiPath(p string) string {
	return fmt.Sprintf("%s/%s/%s", strings.Trim(c.Server, "/"), api_root, p)
}

func GetClient(o io.Writer, e io.Writer, s string) *Client {
	outWriter = o
	errWriter = e
	client := resty.New().R().
		SetHeader("Accept", "application/json")

	return &Client{client, s}
}
