package webhook

import (
	"errors"
	"strings"

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
	errMissingOwner    = errors.New("missing repository.owner.login")
	errMissingName     = errors.New("missing repository.name")
	errNameMismatch    = errors.New("repository.full_name does not match owner.login/name")
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

// validate skips the clone URL, since nothing is cloned for a deletion. The
// filter matches full_name, but the target URL is rendered from owner and
// name, so all three must agree: otherwise a payload could pass the filter as
// one repository and delete another.
func (e *repositoryEvent) validate() error {
	r := e.Repository

	var errs []error
	if r.FullName == "" {
		errs = append(errs, errMissingFullName)
	}
	if r.Owner.Login == "" {
		errs = append(errs, errMissingOwner)
	}
	if r.Name == "" {
		errs = append(errs, errMissingName)
	}
	if len(errs) == 0 && !strings.EqualFold(r.FullName, r.Owner.Login+"/"+r.Name) {
		errs = append(errs, errNameMismatch)
	}

	return errors.Join(errs...)
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
