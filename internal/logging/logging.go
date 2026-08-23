package logging

import (
	"io"
	"log/slog"
)

func New(output io.Writer, level slog.Level, environment string) *slog.Logger {
	var handler slog.Handler
	options := &slog.HandlerOptions{Level: level}
	if environment == "development" {
		handler = slog.NewTextHandler(output, options)
	} else {
		handler = slog.NewJSONHandler(output, options)
	}
	return slog.New(handler).With("service", "forge", "environment", environment)
}
