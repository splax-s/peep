package logger

import (
	"log/slog"
	"os"
)

// New returns a JSON slog.Logger configured for the given service name.
func New(service string, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(h).With("service", service)
}
