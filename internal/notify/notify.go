// Package notify provides a durable, transition-deduplicated webhook outbox.
package notify

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Persistence is a dedicated namespace, never the repository status records.
type Persistence interface {
	Load(context.Context) (map[string][]byte, error)
	Save(context.Context, string, []byte) error
}

const (
	stateHealthy   = "healthy"
	stateRecovered = "recovered"
)

// Event intentionally contains codes and metadata, not tool diagnostics or source text.
type Event struct {
	ID     string    `json:"id"`
	Repo   string    `json:"repository"`
	Target string    `json:"target"`
	State  string    `json:"state"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
	URL    string    `json:"ui_url,omitempty"`
}

// Options supplies all I/O dependencies; credentials remain only in memory.
type Options struct {
	URL, Token, UIURL string
	Client            *http.Client
	Persistence       Persistence
	Now               func() time.Time
}

type (
	observed struct {
		State    string `json:"state"`
		Reason   string `json:"reason"`
		Notified bool   `json:"notified"`
	}
	delivery struct {
		Event     Event     `json:"event"`
		Attempts  int       `json:"attempts"`
		Next      time.Time `json:"next"`
		Exhausted bool      `json:"exhausted"`
	}
	record struct {
		Version    int                 `json:"version"`
		States     map[string]observed `json:"states"`
		Deliveries []delivery          `json:"deliveries"`
	}
)

// Outbox is safe for concurrent publication callbacks and the delivery worker.
type Outbox struct {
	opts    Options
	mu      sync.Mutex
	record  record
	claimed map[string]bool
}

// New restores this endpoint's outbox. Callers namespace persistence by endpoint fingerprint.
func New(ctx context.Context, opts Options) (*Outbox, error) {
	if opts.URL == "" {
		return nil, errors.New("notification URL is required")
	}
	if opts.Client == nil || opts.Persistence == nil {
		return nil, errors.New("notification client and durable storage are required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	box := &Outbox{opts: opts, record: record{Version: 1, States: map[string]observed{}}, claimed: map[string]bool{}}
	records, err := opts.Persistence.Load(ctx)
	if err != nil {
		return nil, errors.New("could not restore notification outbox")
	}
	if data, ok := records["outbox"]; ok {
		if err = json.Unmarshal(data, &box.record); err != nil || box.record.Version != 1 || box.record.States == nil {
			return nil, errors.New("invalid notification outbox")
		}
	}
	return box, nil
}

// Record queues important transitions once; ordinary waiting retries do not page operators.
func (b *Outbox) Record(ctx context.Context, event Event) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := event.Repo + "\x00" + event.Target
	old := b.record.States[key]
	event.Reason = safeReason(event.State, event.Reason)
	if (old.State == event.State && old.Reason == event.Reason) || (old.State == "" && event.State == stateHealthy) {
		return nil
	}
	previous := b.record
	b.record.States = maps.Clone(previous.States)
	b.record.Deliveries = slices.Clone(previous.Deliveries)
	important := slices.Contains([]string{"paused", "blocked", "needs intervention", "exhausted"}, event.State)
	send := important || (event.State == stateHealthy && old.Notified)
	if event.State == stateHealthy {
		delete(b.record.States, key)
	} else {
		b.record.States[key] = observed{State: event.State, Reason: event.Reason, Notified: old.Notified || important}
	}
	if send {
		if err := b.enqueue(event); err != nil {
			b.record = previous
			return err
		}
	}
	if err := b.save(ctx); err != nil {
		b.record = previous
		return err
	}
	return nil
}

func (b *Outbox) enqueue(event Event) error {
	const maxPending = 128
	pending := 0
	for _, d := range b.record.Deliveries {
		if !d.Exhausted {
			pending++
		}
	}
	if pending >= maxPending {
		return errors.New("notification outbox is full")
	}
	event.ID = rand.Text()
	event.At = b.opts.Now()
	if event.State == stateHealthy {
		event.State = stateRecovered
	}
	if b.opts.UIURL != "" {
		event.URL = strings.TrimRight(b.opts.UIURL, "/") + "/"
	}
	b.record.Deliveries = append(b.record.Deliveries, delivery{Event: event, Next: b.opts.Now()})
	return nil
}

func safeReason(state, reason string) string {
	if slices.Contains([]string{"approval_required", "secrets_detected"}, reason) {
		return reason
	}
	switch state {
	case stateHealthy:
		return stateRecovered
	case "paused":
		return "publication_paused"
	case "exhausted":
		return "retry_exhausted"
	default:
		return "sync_failed"
	}
}

func (b *Outbox) save(ctx context.Context) error {
	data, err := json.Marshal(b.record)
	if err != nil {
		return errors.New("could not encode notification state")
	}
	if err = b.opts.Persistence.Save(ctx, "outbox", data); err != nil {
		return errors.New("could not save notification state")
	}
	return nil
}
