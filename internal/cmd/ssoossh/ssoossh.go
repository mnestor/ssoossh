// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/commands/ssh"
	verInfo "github.com/mnestor/ssoossh/internal/version"
)

var rootCmd = &cobra.Command{
	Use:     "ssoossh",
	Short:   "client for managing ssh certificate retrieval",
	Version: verInfo.Version,
}

func init() {
}

func GetCommand(
	ctx context.Context,
	o io.Writer,
	e io.Writer,
	args []string,
) *cobra.Command {
	rootCmd.SetOut(o)
	rootCmd.SetErr(e)

	rootCmd.AddCommand(ssh.GetCommand(o, e))

	return rootCmd
}
