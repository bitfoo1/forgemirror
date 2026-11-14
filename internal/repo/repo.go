// Package repo holds the forge-independent description of a repository that
// flows from the webhook through filtering to mirroring.
package repo

// Repository is a source repository as announced by a push event.
type Repository struct {
	// Owner is the user or organisation that owns the repository.
	Owner string
	// Name is the repository name without the owner.
	Name string
	// FullName is "owner/name".
	FullName string
	// CloneURL is the HTTP(S) URL to clone the repository from.
	CloneURL string
	// Private reports whether the repository is private on the source forge.
	Private bool
	// Fork reports whether the repository is a fork.
	Fork bool
	// Topics are the topics set on the source forge.
	Topics []string
}
