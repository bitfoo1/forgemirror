// Package logging builds the service's structured logger.
package logging

import (
	"io"
	"log/slog"

	"go.bitfoo.de/forgemirror/internal/config"
)

// New returns a logger writing to w in the configured format and level.
// JSON is the default because log collectors parse it without extra rules;
// text is meant for humans running the binary locally. If ring is not nil,
// every record that passes the level is also kept there (web UI).
func New(w io.Writer, cfg config.Log, ring *Ring) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.Level}
	var h slog.Handler = slog.NewJSONHandler(w, opts)
	if cfg.Format == config.LogFormatText {
		h = slog.NewTextHandler(w, opts)
	}
	if ring != nil {
		h = &teeHandler{next: h, ring: ring}
	}

	return slog.New(h)
}
