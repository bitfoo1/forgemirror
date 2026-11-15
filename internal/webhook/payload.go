package webhook

import (
	"errors"

	"go.bitfoo.de/forgemirror/internal/repo"
)

// pushEvent is the subset of Forgejo's push payload forgemirror uses.
// Reference: Forgejo modules/structs/hook.go (PushPayload) and repo.go.
type pushEvent struct {
	Ref        string            `json:"ref"`
	Before     string            `json:"before"`
	After      string            `json:"after"`
	Repository payloadRepository `json:"repository"`
}

type payloadRepository struct {
	Name     string       `json:"name"`
	FullName string       `json:"full_name"`
	Owner    payloadOwner `json:"owner"`
	Private  bool         `json:"private"`
	Fork     bool         `json:"fork"`
	CloneURL string       `json:"clone_url"`
	Topics   []string     `json:"topics"`
}

type payloadOwner struct {
	Login string `json:"login"`
}

var (
	errMissingRef      = errors.New("missing ref")
	errMissingFullName = errors.New("missing repository.full_name")
	errMissingCloneURL = errors.New("missing repository.clone_url")
)

func (e *pushEvent) validate() error {
	var errs []error
	if e.Ref == "" {
		errs = append(errs, errMissingRef)
	}
	if e.Repository.FullName == "" {
		errs = append(errs, errMissingFullName)
	}
	if e.Repository.CloneURL == "" {
		errs = append(errs, errMissingCloneURL)
	}

	return errors.Join(errs...)
}

// repositoryEvent is the subset of Forgejo's repository payload forgemirror
// uses; it carries the same repository object as a push.
type repositoryEvent struct {
	Action     string            `json:"action"`
	Repository payloadRepository `json:"repository"`
}

// validate requires only the full name: nothing is cloned for a deletion, so
// the clone URL is irrelevant.
func (e *repositoryEvent) validate() error {
	if e.Repository.FullName == "" {
		return errMissingFullName
	}

	return nil
}

func (e *repositoryEvent) repository() repo.Repository {
	return (&pushEvent{Repository: e.Repository}).repository()
}

func (e *pushEvent) repository() repo.Repository {
	r := e.Repository

	return repo.Repository{
		Owner:    r.Owner.Login,
		Name:     r.Name,
		FullName: r.FullName,
		CloneURL: r.CloneURL,
		Private:  r.Private,
		Fork:     r.Fork,
		Topics:   r.Topics,
	}
}
