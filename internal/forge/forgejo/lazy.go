package forgejo

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
)

// DefaultFailureBackoff is how long a failed connection attempt is reused
// before the next caller probes Forgejo again.
const DefaultFailureBackoff = 10 * time.Second

// Lazy is a [Client] that connects on first use. New probes the server
// version, so connecting at startup would make the service crash-loop
// whenever Forgejo is briefly unreachable; Lazy defers that to the first run
// and retries on later uses until a connection succeeds.
type Lazy struct {
	httpClient *http.Client
	baseURL    string
	token      config.Secret

	// lock is a one-slot channel instead of a sync.Mutex so that callers
	// waiting behind a hanging probe (up to the HTTP timeout) can give up
	// when their own context ends. It serialises connection attempts so
	// parallel target steps do not probe the server once each, and guards
	// the fields below.
	lock chan struct{}
	// client is cached only after success.
	client *Client
	// lastErr is the most recent connection failure, reused within
	// failureBackoff of failedAt: a scheduled sweep dispatches every
	// repository at once, and without it each queued caller would run its
	// own probe against a hanging Forgejo, one HTTP timeout after another.
	lastErr        error
	failedAt       time.Time
	failureBackoff time.Duration
	now            func() time.Time
}

var _ forge.Target = (*Lazy)(nil)

// NewLazy returns a [Lazy] client; it makes no request.
func NewLazy(httpClient *http.Client, baseURL string, token config.Secret) *Lazy {
	return &Lazy{
		httpClient:     httpClient,
		baseURL:        baseURL,
		token:          token,
		lock:           make(chan struct{}, 1),
		failureBackoff: DefaultFailureBackoff,
		now:            time.Now,
	}
}

func (l *Lazy) connect(ctx context.Context) (*Client, error) {
	select {
	case l.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("forgejo connect: waiting for connection: %w", ctx.Err())
	}
	defer func() { <-l.lock }()

	if l.client != nil {
		return l.client, nil
	}
	if l.lastErr != nil && l.now().Sub(l.failedAt) < l.failureBackoff {
		return nil, l.lastErr
	}

	c, err := New(ctx, l.httpClient, l.baseURL, l.token)
	if err != nil {
		// A probe aborted by this caller's own context says nothing about
		// Forgejo; caching it would fail unrelated callers.
		if ctx.Err() == nil {
			l.lastErr, l.failedAt = err, l.now()
		}

		return nil, err
	}
	l.client, l.lastErr = c, nil

	return c, nil
}

// Source returns the forge.Source view, connecting on first use as well.
func (l *Lazy) Source() forge.Source { return lazySource{l} }

// Capabilities implements forge.Target. They do not depend on the server, so
// no connection is needed.
func (l *Lazy) Capabilities() forge.Capabilities { return new(Client).Capabilities() }

// Get implements forge.Target.
func (l *Lazy) Get(ctx context.Context, a forge.Address) (forge.Repo, error) {
	c, err := l.connect(ctx)
	if err != nil {
		return forge.Repo{}, err
	}

	return c.Get(ctx, a)
}

// Create implements forge.Target.
func (l *Lazy) Create(ctx context.Context, a forge.Address, r forge.Repo) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.Create(ctx, a, r)
}

// Update implements forge.Target.
func (l *Lazy) Update(ctx context.Context, a forge.Address, s forge.Settings) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.Update(ctx, a, s)
}

// Labels implements forge.Target.
func (l *Lazy) Labels(ctx context.Context, a forge.Address) ([]forge.Label, error) {
	c, err := l.connect(ctx)
	if err != nil {
		return nil, err
	}

	return c.Labels(ctx, a)
}

// CreateLabel implements forge.Target.
func (l *Lazy) CreateLabel(ctx context.Context, a forge.Address, lb forge.Label) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.CreateLabel(ctx, a, lb)
}

// UpdateLabel implements forge.Target.
func (l *Lazy) UpdateLabel(ctx context.Context, a forge.Address, lb forge.Label) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.UpdateLabel(ctx, a, lb)
}

// DeleteLabel implements forge.Target.
func (l *Lazy) DeleteLabel(ctx context.Context, a forge.Address, name string) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.DeleteLabel(ctx, a, name)
}

// Delete implements forge.Target.
func (l *Lazy) Delete(ctx context.Context, a forge.Address) error {
	c, err := l.connect(ctx)
	if err != nil {
		return err
	}

	return c.Delete(ctx, a)
}

type lazySource struct{ l *Lazy }

func (s lazySource) Repo(ctx context.Context, owner, name string) (forge.Repo, error) {
	c, err := s.l.connect(ctx)
	if err != nil {
		return forge.Repo{}, err
	}

	return c.Source().Repo(ctx, owner, name)
}

func (s lazySource) Labels(ctx context.Context, owner, name string) ([]forge.Label, error) {
	c, err := s.l.connect(ctx)
	if err != nil {
		return nil, err
	}

	return c.Source().Labels(ctx, owner, name)
}

func (s lazySource) Repos(ctx context.Context) ([]repo.Repository, error) {
	c, err := s.l.connect(ctx)
	if err != nil {
		return nil, err
	}

	return c.Source().Repos(ctx)
}
