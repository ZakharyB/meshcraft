// Package observability provides structured logging for all binaries.
package observability

import (
	"log/slog"
	"os"
)

// New returns a slog logger writing JSON to stderr with a component tag.
func New(component string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h).With("component", component)
}
