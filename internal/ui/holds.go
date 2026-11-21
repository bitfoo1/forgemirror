package ui

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/hold"
	"go.bitfoo.de/forgemirror/internal/status"
)

const (
	holdActionPath       = "/actions/hold"
	resumeHoldActionPath = "/actions/resume-hold"
	trueValue            = "true"
)

func (h *handler) writeHoldError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, hold.ErrInvalid) {
		code = http.StatusBadRequest
	}
	if errors.Is(err, control.ErrUnknownTarget) || errors.Is(err, control.ErrUnknownRepository) ||
		errors.Is(err, status.ErrUnknownHold) {
		code = http.StatusNotFound
	}
	if notifyErr := triggerNotice(w, err.Error(), true, false); notifyErr != nil {
		http.Error(w, "Could not encode action acknowledgement", http.StatusInternalServerError)
		return
	}
	http.Error(w, err.Error(), code)
}

func (h *handler) controlsData(data *pageData) {
	for _, repository := range data.Repos {
		if repository.Actionable {
			data.HoldRepositories = append(data.HoldRepositories, repository.Name)
		}
	}
	slices.Sort(data.HoldRepositories)
	for _, p := range data.Pauses {
		if p.Scope.AllTargets && p.Scope.AllRepositories {
			data.AllPublicationPaused = true
		}
	}
}

func (h *handler) holdAction(r *http.Request) (string, error) {
	if r.URL.Path == resumeHoldActionPath {
		id := r.PostForm.Get("id")
		if id == "" || len(id) > 80 {
			return "", hold.ErrInvalid
		}
		if err := h.opts.Actions.ResumeHold(r.Context(), id); err != nil {
			return "", err
		}
		return "Hold removed; fresh sync requested. Other holds and approvals still apply.", nil
	}
	for _, key := range []string{"all_targets", "all_repositories"} {
		if values := r.PostForm[key]; len(values) > 1 ||
			(len(values) == 1 && values[0] != "" && values[0] != trueValue) {
			return "", hold.ErrInvalid
		}
	}
	scope := hold.Scope{
		AllTargets:      r.PostForm.Get("all_targets") == trueValue,
		AllRepositories: r.PostForm.Get("all_repositories") == trueValue,
		Targets:         r.PostForm["targets"],
		Repositories:    splitRepositoryScopes(r.PostForm["repositories"]),
	}
	if err := scope.Validate(); err != nil {
		return "", err
	}
	if len(r.PostForm.Get("reason")) > hold.MaxReasonBytes {
		return "", hold.ErrInvalid
	}
	until, err := h.holdExpiry(r)
	if err != nil {
		return "", err
	}
	if err = h.opts.Actions.PauseScope(r.Context(), scope, r.PostForm.Get("reason"), until); err != nil {
		return "", err
	}
	return "Publication hold created. Comparisons continue.", nil
}

func splitRepositoryScopes(values []string) []string {
	var scopes []string
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
			if term := strings.TrimSpace(part); term != "" {
				scopes = append(scopes, term)
			}
		}
	}
	return scopes
}

func (h *handler) holdExpiry(r *http.Request) (time.Time, error) {
	choice, text := r.PostForm.Get("duration_choice"), r.PostForm.Get("duration")
	if choice == "until-resumed" {
		if text != "" {
			return time.Time{}, hold.ErrInvalid
		}
		return time.Time{}, nil
	}
	if choice != "" && choice != "custom" {
		if text != "" {
			return time.Time{}, hold.ErrInvalid
		}
		text = choice
	}
	if text == "" {
		if choice == "custom" {
			return time.Time{}, hold.ErrInvalid
		}
		return time.Time{}, nil
	}
	duration, err := time.ParseDuration(text)
	if err != nil || duration <= 0 {
		return time.Time{}, fmt.Errorf("%w: duration must be positive, for example 45m or 6h", hold.ErrInvalid)
	}
	return h.opts.Now().Add(duration), nil
}
