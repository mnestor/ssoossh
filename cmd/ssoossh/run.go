// Created By Mike Nestor <me@mikenestor.org>
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"

	"github.com/mnestor/ssoossh/internal/cmd/ssoossh"
)

func run(ctx context.Context, o io.Writer, e io.Writer, args []string) int {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	var ret = 1

	cmd, err := ssoossh.NewRootCommand(ctx, o, e, args)

	go func() {
		if err != nil {
			fmt.Fprint(os.Stderr, err.Error())
		} else {
			// cmd.SetContext(ctx)
			// log.Printf("run: %v", ctx)
			cmd.ExecuteContext(ctx)
			log.Printf("run: %v", cmd.Context())
			ret = 0
		}
		cancel()
	}()

	<-ctx.Done()

	return ret
}
