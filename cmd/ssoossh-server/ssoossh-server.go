// Created By Mike Nestor <me@mikenestor.org>
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/mnestor/ssoossh/internal/cmd/ssoossh-server"
)

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Stdout, os.Stderr, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o io.Writer, e io.Writer, args []string) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	cmd := ssoossh.GetCommand(ctx, o, e, args)
	return cmd.Execute()
}
