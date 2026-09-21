// Package logging builds the structured JSON logger used by every process.
package logging

import (
	"io"
	"log/slog"
)

// New returns a JSON logger writing to w at the given level
// ("debug", "info", "warn", or "error"; anything else means "info").
func New(w io.Writer, level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(level)}))
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
