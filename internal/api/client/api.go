// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/mnestor/ssoossh/internal/ssh"
	"github.com/mnestor/ssoossh/internal/version"
	"resty.dev/v3"
)

type ClientI interface {
	GetCA() (string, error)
	GetCertificate(string) (string, error)
	PostPubKey(ssh.KeyPairI) (string, error)
}

type Client struct {
	// ClientI
	*resty.Request
}

func NewClient(ctx context.Context, s string) ClientI {
	client := resty.New().
		SetContext(ctx).
		SetBaseURL(fmt.Sprintf("%s/api/%s/", strings.Trim(s, "/"), version.ApiPath)).
		SetHeader("Accept", "application/json").
		R(). // this passes a contextWithoutCancel so set it with our cancel
		SetContext(ctx)

	return &Client{
		Request: client,
	}
}
