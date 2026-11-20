package state

import "context"

// Records is a namespace view of the owner's connection; it does not own its lifetime.
type Records struct {
	owner *DB
	scope string
}

// Namespace separates non-status records without opening a second database.
func (d *DB) Namespace(name string) *Records {
	return &Records{owner: d, scope: d.scope + "\x00" + name}
}

// Load returns only this namespace's records.
func (r *Records) Load(ctx context.Context) (map[string][]byte, error) {
	view := DB{db: r.owner.db, scope: r.scope}
	return view.Load(ctx)
}

// Save atomically replaces a record in this namespace.
func (r *Records) Save(ctx context.Context, name string, payload []byte) error {
	view := DB{db: r.owner.db, scope: r.scope}
	return view.Save(ctx, name, payload)
}
