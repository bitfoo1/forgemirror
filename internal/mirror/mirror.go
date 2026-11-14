// Package mirror copies repositories between forges with the git binary.
//
// A mirror run clones the source once ([Mirrorer.Clone]) and pushes that
// clone to every target ([Workspace.Push]), instead of cloning per target:
// clones dominate the cost for large repositories.
package mirror

import (
	"context"

	"go.bitfoo.de/forgemirror/internal/config"
)

// Remote is a git remote and the credentials to access it.
type Remote struct {
	// URL is an HTTP(S) URL without credentials.
	URL string
	// Username and Token are sent as HTTP Basic auth; an empty Token means
	// anonymous access.
	Username string
	Token    config.Secret
}

// Mirrorer creates local copies of source repositories.
type Mirrorer interface {
	// Clone creates a bare mirror clone of src. The caller must Close the
	// returned Workspace.
	Clone(ctx context.Context, src Remote) (Workspace, error)
}

// Workspace is a local mirror clone.
type Workspace interface {
	// Push makes dst's branches and tags identical to the clone's: it
	// creates, force-updates and deletes refs as needed. Safe for concurrent
	// use by multiple goroutines.
	Push(ctx context.Context, dst Remote) error
	// Close deletes the clone. It is idempotent.
	Close() error
}
