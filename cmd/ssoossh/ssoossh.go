// Created By Mike Nestor <me@mikenestor.org>
package main

import (
	"context"
	"os"
)

func main() {
	o := os.Stdout
	e := os.Stderr
	args := os.Args
	ctx := context.Background()
	if code := run(ctx, o, e, args[1:]); code != 0 {
		os.Exit(code)
	}
}
