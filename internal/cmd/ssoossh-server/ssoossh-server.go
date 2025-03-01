// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd"
	"github.com/mnestor/ssoossh/internal/config"
	verInfo "github.com/mnestor/ssoossh/internal/version"
)

var rootCmd = &cobra.Command{
	Use:     "ssoossh-server",
	Short:   "server for distributing ssh certificates",
	Version: verInfo.Version,
	RunE:    runCommand,
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

	return rootCmd
}

func runCommand(cmd *cobra.Command, args []string) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	serverCtx, serverCtxCancel := context.WithCancel(cmd.Context())

	_ = config.LoadConfig(true)
	webServer, err := httpd.NewServer()
	if err != nil {
		slog.Error("", slog.Any("error", err))
	}

	slog.Info("starting...")
	go func() {
		for {
			select {
			case s := <-sigs:
				switch s {
				case syscall.SIGHUP:
					slog.Info("hot reload")
					if err := config.LoadConfig(false); err != nil {
						slog.Error("unable to reload config", slog.Any("error", err))
					}

				// kill -SIGINT XXXX or Ctrl+c
				case syscall.SIGINT:
					slog.Info("sig int")
					serverCtxCancel()

					// kill -SIGTERM XXXX
				case syscall.SIGTERM:
					slog.Info("force stop")
					serverCtxCancel()

				// kill -SIGQUIT XXXX
				case syscall.SIGQUIT:
					slog.Info("quit")
				default:
					slog.Info("unhandled signal", slog.Any("signal", s))
				}

			case <-cmd.Context().Done():
				slog.Info("exiting")

			}
		}
	}()

	go func() {
		err := webServer.Listen()
		if err != nil {
			panic(err)
		}
	}()

	<-serverCtx.Done()
	slog.Info("done")
	webServer.Close()

	return nil
}
