// Created By Mike Nestor <me@mikenestor.org>
package main

import (
	"context"
	"io"
	"os"
	"os/signal"

	"github.com/mnestor/ssoossh/internal/cmd/ssoossh"
)

func run(ctx context.Context, o io.Writer, e io.Writer, args []string) int {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	var ret = 1

	cmd := ssoossh.NewRootCommand(ctx, o, e, args)

	go func() {
		if err := cmd.ExecuteContext(ctx); err == nil {
			ret = 0
		}
		cancel()
	}()

	<-ctx.Done()

	return ret
}
