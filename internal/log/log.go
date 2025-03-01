// Created by Mike Nestor <me@mikenestor.org>
package log

import (
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
)

type LogSettings struct {
	Level int  `mapstructure:"level"`
	Json  bool `mapstructure:"json"`
}

var logger *slog.Logger

func SetupLogger(c LogSettings) {
	opts := &slog.HandlerOptions{
		Level: slog.Level(c.Level),

		// Log level add source if INFO/DEBUG
		AddSource: c.Level < -4,
	}

	var handler slog.Handler
	if c.Json {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else if isatty.IsTerminal(os.Stderr.Fd()) {
		handler = tint.NewHandler(os.Stdout, &tint.Options{
			NoColor: false,
			Level:   slog.Level(c.Level),

			// Log level add source if INFO/DEBUG
			AddSource: c.Level < -4,
		})
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger = slog.New(handler)
	slog.SetDefault(logger)
}

func GetLogger() *slog.Logger {
	return logger
}
