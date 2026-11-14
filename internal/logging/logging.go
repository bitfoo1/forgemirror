// Package logging builds the service's structured logger.
package logging

import (
	"io"
	"log/slog"

	"go.bitfoo.de/forgemirror/internal/config"
)

// New returns a logger writing to w in the configured format and level.
// JSON is the default because log collectors parse it without extra rules;
// text is meant for humans running the binary locally.
func New(w io.Writer, cfg config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.Level}
	if cfg.Format == config.LogFormatText {
		return slog.New(slog.NewTextHandler(w, opts))
	}

	return slog.New(slog.NewJSONHandler(w, opts))
}
