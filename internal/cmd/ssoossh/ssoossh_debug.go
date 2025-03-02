//go:build DEV
// +build DEV

// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"log/slog"
	"os"

	"github.com/spf13/viper"
)

const DEBUG = true

func getViper() *viper.Viper {
	if os.Getenv("VIPER_DEBUG") == "1" {
		handler := slog.NewTextHandler(errWriter, &slog.HandlerOptions{
			Level:     slog.Level(slog.LevelDebug),
			AddSource: true,
		})
		logger := slog.New(handler)
		slog.SetDefault(logger)

		return viper.NewWithOptions(viper.WithLogger(logger))
	}
	return viper.New()
}
