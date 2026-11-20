package ui

import (
	"errors"
	"net/http"
)

func (h *handler) approvalAction(r *http.Request) (string, error) {
	repo, target, identity := r.PostForm.Get("repo"), r.PostForm.Get("target"), r.PostForm.Get("identity")
	if repo == "" || target == "" || identity == "" {
		return "", errors.New("repository, target and current destination identity are required")
	}
	return "Destination approved; fresh sync requested for " + repo + ".", h.opts.Actions.Approve(
		r.Context(),
		repo,
		target,
		identity,
	)
}
