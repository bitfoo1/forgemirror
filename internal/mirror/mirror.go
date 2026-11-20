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
	// Refs lists the remote's branches and tags as ref name to object id,
	// without cloning. The scheduled run compares these maps to skip targets
	// that are already in sync. An empty remote yields an empty map.
	Refs(ctx context.Context, r Remote) (map[string]string, error)
}

// Workspace is a local mirror clone.
type Workspace interface {
	// FetchLFS downloads objects reachable from the workspace's published refs.
	// Call once before concurrent PushLFS calls.
	FetchLFS(ctx context.Context, src Remote) error
	// PushLFS uploads the downloaded objects before publishing Git refs.
	PushLFS(ctx context.Context, dst Remote) error
	// Push makes dst's branches and tags identical to the clone's: it
	// creates, force-updates and deletes refs as needed, and returns the refs
	// it changed. Safe for concurrent use by multiple goroutines.
	Push(ctx context.Context, dst Remote) ([]RefUpdate, error)
	// Close deletes the clone. It is idempotent.
	Close() error
}

// Kinds of [RefUpdate].
const (
	RefCreated = "create"
	RefUpdated = "update"
	RefForced  = "force"
	RefDeleted = "delete"
)

// RefUpdate is one ref a push changed on the target, as git reported it.
type RefUpdate struct {
	// Ref is the ref name on the target, e.g. "refs/heads/main".
	Ref string
	// Kind is one of RefCreated, RefUpdated, RefForced, RefDeleted.
	Kind string
	// Summary is git's description, e.g. "0e67ebf..2eca933" or "[new tag]".
	Summary string
}
