// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"io"

	"resty.dev/v3"
)

var (
	outWriter io.Writer
	errWriter io.Writer
)

type Server struct {
	*resty.Client
}

func GetClient(o io.Writer, e io.Writer) *Server {
	outWriter = o
	errWriter = e
	server := resty.New().
		SetHeader("Accept", "application/json")

	return &Server{server}
}
