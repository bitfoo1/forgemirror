package logging

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Entry is one log record as kept for the web UI.
type Entry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	// Attrs are flattened: group names are joined to the key with dots.
	Attrs []Attr
}

// Attr is a resolved attribute of an [Entry].
type Attr struct {
	Key   string
	Value string
}

// Ring keeps the most recent log records in a fixed-size buffer. It is safe
// for concurrent use.
type Ring struct {
	mu      sync.Mutex
	entries []Entry
	next    int
	full    bool
}

// NewRing returns a [Ring] holding at most capacity records (at least one).
func NewRing(capacity int) *Ring {
	return &Ring{entries: make([]Entry, max(capacity, 1))}
}

// Entries returns the kept records, newest first.
func (r *Ring) Entries() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []Entry
	if r.full {
		out = append(slices.Clone(r.entries[r.next:]), r.entries[:r.next]...)
	} else {
		out = slices.Clone(r.entries[:r.next])
	}
	slices.Reverse(out)

	return out
}

func (r *Ring) add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries[r.next] = e
	r.next = (r.next + 1) % len(r.entries)
	if r.next == 0 {
		r.full = true
	}
}

// teeHandler passes every record to next and keeps a copy in ring. Values
// are resolved with [slog.Value.Resolve], which applies LogValue, so
// redacting types such as config.Secret stay redacted in the copy too.
type teeHandler struct {
	next   slog.Handler
	ring   *Ring
	prefix string // open groups, "a.b."
	attrs  []Attr
}

func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *teeHandler) Handle(ctx context.Context, rec slog.Record) error {
	e := Entry{Time: rec.Time, Level: rec.Level, Message: rec.Message, Attrs: slices.Clone(h.attrs)}
	rec.Attrs(func(a slog.Attr) bool {
		e.Attrs = appendAttr(e.Attrs, h.prefix, a)

		return true
	})
	h.ring.add(e)

	//nolint:wrapcheck // a pass-through handler must not change next's errors
	return h.next.Handle(ctx, rec)
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.next = h.next.WithAttrs(attrs)
	c.attrs = slices.Clone(h.attrs)
	for _, a := range attrs {
		c.attrs = appendAttr(c.attrs, h.prefix, a)
	}

	return &c
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.next = h.next.WithGroup(name)
	c.prefix = h.prefix + name + "."

	return &c
}

func appendAttr(dst []Attr, prefix string, a slog.Attr) []Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return dst
	}
	if a.Value.Kind() == slog.KindGroup {
		groupPrefix := prefix
		if a.Key != "" {
			groupPrefix = prefix + a.Key + "."
		}
		for _, ga := range a.Value.Group() {
			dst = appendAttr(dst, groupPrefix, ga)
		}

		return dst
	}

	return append(dst, Attr{Key: strings.TrimSuffix(prefix+a.Key, "."), Value: a.Value.String()})
}
