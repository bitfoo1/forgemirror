package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"go.bitfoo.de/forgemirror/internal/retry"
)

// Run processes the independent outbox until cancellation. Errors are reported
// without endpoint URLs, credentials or response bodies.
func (b *Outbox) Run(ctx context.Context, onError func()) {
	if b == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := b.DeliverDue(ctx); err != nil && onError != nil {
			onError()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// DeliverDue attempts one eligible event without holding the outbox lock over HTTP.
func (b *Outbox) DeliverDue(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	index := b.nextDelivery()
	if index < 0 {
		b.mu.Unlock()
		return nil
	}
	item := b.record.Deliveries[index]
	b.claimed[item.Event.ID] = true
	b.mu.Unlock()
	success, notBefore := b.send(ctx, item.Event)
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.claimed, item.Event.ID)
	index = slices.IndexFunc(b.record.Deliveries, func(d delivery) bool { return d.Event.ID == item.Event.ID })
	if index < 0 {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	if success {
		b.record.Deliveries = slices.Delete(b.record.Deliveries, index, index+1)
	} else {
		const maxAttempts = 5
		item.Attempts++
		item.Exhausted = item.Attempts >= maxAttempts
		item.Next = b.opts.Now().Add(retry.Delay(item.Attempts, 0))
		if notBefore.After(item.Next) {
			item.Next = notBefore
		}
		b.record.Deliveries[index] = item
	}
	b.pruneExhausted()
	if err := b.save(ctx); err != nil {
		return err
	}
	if !success {
		return errors.New("notification delivery failed; see outbox retry policy")
	}
	return nil
}

func (b *Outbox) nextDelivery() int {
	seen := map[[2]string]bool{}
	for i, d := range b.record.Deliveries {
		if d.Exhausted {
			continue
		}
		key := [2]string{d.Event.Repo, d.Event.Target}
		if seen[key] {
			continue
		}
		seen[key] = true
		if !d.Next.After(b.opts.Now()) && !b.claimed[d.Event.ID] {
			return i
		}
	}
	return -1
}

func (b *Outbox) pruneExhausted() {
	const maxFailed = 32
	failed := 0
	for i, d := range slices.Backward(b.record.Deliveries) {
		if d.Exhausted {
			failed++
			if failed > maxFailed {
				b.record.Deliveries = slices.Delete(b.record.Deliveries, i, i+1)
			}
		}
	}
}

func (b *Outbox) send(ctx context.Context, event Event) (bool, time.Time) {
	data, err := json.Marshal(event)
	if err != nil {
		return false, time.Time{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.opts.URL, bytes.NewReader(data))
	if err != nil {
		return false, time.Time{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", event.ID)
	if b.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.opts.Token)
	}
	client := *b.opts.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return false, time.Time{}
	}
	defer response.Body.Close()
	const maxDrain = 1024
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxDrain))
	success := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	bound := retry.NotBefore(retry.HTTP(errors.New("delivery rejected"), response, b.opts.Now()))
	return success, bound
}
