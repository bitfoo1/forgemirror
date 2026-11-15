package logging_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/logging"
)

func messages(entries []logging.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Message
	}

	return out
}

func TestRingCapacity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		capacity int
		logged   int
		want     []string
	}{
		{name: "empty", capacity: 3, logged: 0, want: []string{}},
		{name: "partly filled", capacity: 3, logged: 2, want: []string{"m1", "m0"}},
		{name: "exactly full", capacity: 3, logged: 3, want: []string{"m2", "m1", "m0"}},
		{name: "wrapped", capacity: 3, logged: 7, want: []string{"m6", "m5", "m4"}},
		{name: "capacity below one", capacity: 0, logged: 2, want: []string{"m1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ring := logging.NewRing(tc.capacity)
			logger := logging.New(&bytes.Buffer{}, config.Log{Level: slog.LevelInfo}, ring)
			for i := range tc.logged {
				logger.Info(fmt.Sprintf("m%d", i))
			}

			if got := messages(ring.Entries()); !slices.Equal(got, tc.want) {
				t.Errorf("entries = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRingKeepsWhatPassesTheLevel(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	ring := logging.NewRing(10)
	logger := logging.New(&out, config.Log{Level: slog.LevelWarn, Format: config.LogFormatText}, ring)
	logger.Info("dropped")
	logger.Warn("kept")

	entries := ring.Entries()
	if got := messages(entries); !slices.Equal(got, []string{"kept"}) {
		t.Fatalf("entries = %q, want [kept]", got)
	}
	if entries[0].Level != slog.LevelWarn || entries[0].Time.IsZero() {
		t.Errorf("entry = %+v, want level WARN and a time", entries[0])
	}
	if !strings.Contains(out.String(), "msg=kept") {
		t.Errorf("normal output = %q, want the record there too", out.String())
	}
}

func TestRingAttributes(t *testing.T) {
	t.Parallel()

	ring := logging.NewRing(10)
	logger := logging.New(&bytes.Buffer{}, config.Log{Level: slog.LevelInfo}, ring).
		With(slog.String("server", "web")).
		WithGroup("").
		WithGroup("req").
		With(slog.Int("id", 7))
	logger.LogAttrs(t.Context(), slog.LevelInfo, "hello",
		slog.Any("token", config.Secret("hunter2")),
		slog.Any("error", errors.New("boom")),
		slog.Group("peer", slog.String("addr", "10.0.0.1")),
		slog.Group("", slog.String("inline", "yes")),
		slog.Attr{},
	)

	entries := ring.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	want := []logging.Attr{
		{Key: "server", Value: "web"},
		{Key: "req.id", Value: "7"},
		{Key: "req.token", Value: "[REDACTED]"},
		{Key: "req.error", Value: "boom"},
		{Key: "req.peer.addr", Value: "10.0.0.1"},
		{Key: "req.inline", Value: "yes"},
	}
	if got := entries[0].Attrs; !slices.Equal(got, want) {
		t.Errorf("attrs = %+v\nwant %+v", got, want)
	}
}

func TestRingConcurrentUse(t *testing.T) {
	t.Parallel()

	ring := logging.NewRing(16)
	logger := logging.New(io.Discard, config.Log{Level: slog.LevelInfo}, ring)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				logger.Info("m")
				_ = ring.Entries()
			}
		})
	}
	wg.Wait()

	if n := len(ring.Entries()); n != 16 {
		t.Errorf("entries = %d, want 16", n)
	}
}
