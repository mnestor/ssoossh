// Created By Mike Nestor <me@mikenestor.org>
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mnestor/ssoossh/internal/cmd/ssoossh"
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

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	runCtx, runCtxCancel := context.WithCancel(ctx)

	cmd := ssoossh.GetCommand(ctx, o, e, args)
	var err error
	go func() {
		err = cmd.Execute()
		runCtxCancel()
	}()

	<-runCtx.Done()

	return err
}
