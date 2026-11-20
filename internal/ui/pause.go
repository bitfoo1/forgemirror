package ui

import (
	"errors"
	"net/http"
	"time"
)

func (h *handler) pauseAction(r *http.Request) (string, error) {
	repo, target := r.PostForm.Get("repo"), r.PostForm.Get("target")
	if r.PostForm.Get("mode") == "resume" {
		return "Resume requested for " + h.forgeIdentity(
				target,
			).Label + ".", h.opts.Actions.Resume(
				r.Context(),
				repo,
				target,
			)
	}
	reason := r.PostForm.Get("reason")
	const maxReasonBytes = 256
	if len(reason) > maxReasonBytes {
		return "", errors.New("pause reason exceeds 256 bytes")
	}
	var until time.Time
	if text := r.PostForm.Get("duration"); text != "" {
		duration, err := time.ParseDuration(text)
		if err != nil || duration <= 0 {
			return "", errors.New("pause duration must be positive, for example 30m or 2h")
		}
		until = h.opts.Now().Add(duration)
	}
	return "Pause requested for " + h.forgeIdentity(
			target,
		).Label + ".", h.opts.Actions.Pause(
			r.Context(),
			repo,
			target,
			reason,
			until,
		)
}
