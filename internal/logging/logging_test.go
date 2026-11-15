package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/logging"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      config.Log
		wantJSON bool
	}{
		{name: "json", cfg: config.Log{Level: slog.LevelInfo, Format: config.LogFormatJSON}, wantJSON: true},
		{name: "text", cfg: config.Log{Level: slog.LevelInfo, Format: config.LogFormatText}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logging.New(&buf, tc.cfg, nil).Info("hello", "k", "v")

			isJSON := json.Valid(bytes.TrimSpace(buf.Bytes()))
			if isJSON != tc.wantJSON {
				t.Errorf("output %q: JSON = %v, want %v", buf.String(), isJSON, tc.wantJSON)
			}
		})
	}
}

func TestNewLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := logging.New(&buf, config.Log{Level: slog.LevelWarn, Format: config.LogFormatText}, nil)
	logger.Info("dropped")
	logger.Warn("kept")

	if strings.Contains(buf.String(), "dropped") || !strings.Contains(buf.String(), "kept") {
		t.Errorf("output = %q, want only the warning", buf.String())
	}
}
