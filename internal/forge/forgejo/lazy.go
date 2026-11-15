package forgejo

import (
	"context"
	"net/http"
	"sync"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
)

// Lazy is a [Client] that connects on first use. New probes the server
// version, so connecting at startup would make the service crash-loop
// whenever Forgejo is briefly unreachable; Lazy defers that to the first run
// and retries on every later use until a connection succeeds.
type Lazy struct {
	httpClient *http.Client
	baseURL    string
	token      config.Secret

	// mu serialises connection attempts so parallel target steps do not
	// probe the server once each; client is cached only after success.
	mu     sync.Mutex
	client *Client
}

var _ forge.Target = (*Lazy)(nil)

// NewLazy returns a [Lazy] client; it makes no request.
func NewLazy(httpClient *http.Client, baseURL string, token config.Secret) *Lazy {
	return &Lazy{httpClient: httpClient, baseURL: baseURL, token: token}
}

func (l *Lazy) connect(ctx context.Context) (*Client, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.client != nil {
		return l.client, nil
	}

	c, err := New(ctx, l.httpClient, l.baseURL, l.token)
	if err != nil {
		return nil, err
	}
	l.client = c

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
