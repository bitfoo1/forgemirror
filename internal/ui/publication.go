package ui

import (
	"slices"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/publication"
	"go.bitfoo.de/forgemirror/internal/retry"
)

func (h *handler) explainPublications(data *pageData) {
	jobs := make(map[[2]string]retry.Entry, len(data.Retries))
	for _, job := range data.Retries {
		jobs[[2]string{job.Repo, job.Target}] = job
	}
	for i := range data.Repos {
		repository := &data.Repos[i]
		for _, configured := range h.opts.Config.Targets {
			if !slices.ContainsFunc(repository.Targets, func(t targetView) bool { return t.Name == configured.Name }) {
				repository.Targets = append(repository.Targets, targetView{Name: configured.Name})
			}
		}
		for j := range repository.Targets {
			target := &repository.Targets[j]
			h.attachPublicationPolicy(data, repository, target)
			input := h.publicationInput(data, repository, target)
			if input.TargetRemoved {
				target.Actionable = false
			}
			if job, ok := jobs[[2]string{repository.Name, target.Name}]; ok {
				input.Retry = &job
			}
			view := publication.Explain(input)
			target.Publication = &view
			target.Badge = targetBadge(repository, target, input.TargetRemoved)
			if view.Held {
				repository.HeldTargets++
				data.Counts.HeldTargets++
			}
		}
		repository.ChangeGroups = groupedChanges(repository.Targets)
		for _, group := range repository.ChangeGroups {
			repository.DistinctChanges += group.Plan.Changes()
		}
	}
}

func (h *handler) attachPublicationPolicy(data *pageData, repository *repoView, target *targetView) {
	target.Repo = repository.Name
	target.Actionable = data.Actions && repository.Actionable
	if pause, ok := h.opts.Store.Paused(repository.Name, target.Name); ok {
		target.Pause = &pause
	}
	if target.Plan != nil && target.Plan.PublicationID != "" &&
		h.opts.Store.Approved(repository.Name, target.Name, target.Plan.PublicationID) {
		current := *target.Plan
		current.ApprovalRequired = false
		target.Plan = &current
	}
}

func (h *handler) publicationInput(data *pageData, repository *repoView, target *targetView) publication.Input {
	input := publication.Input{
		Known:            repository.Actionable,
		Auto:             data.Auto,
		Stale:            repository.Stale,
		Running:          repository.Running,
		Applying:         repository.Applying,
		Queued:           repository.Queued,
		Plan:             target.Plan,
		Last:             target.Last,
		Pause:            target.Pause,
		Now:              h.opts.Now(),
		LastSuccess:      h.opts.Store.LastSuccessfulSync(repository.Name, target.Name),
		StateUnavailable: data.Snapshot.Error != "",
	}
	input.TargetRemoved = !slices.ContainsFunc(
		h.opts.Config.Targets,
		func(t config.Target) bool { return t.Name == target.Name },
	)
	comparison := repository.Current
	if comparison == nil && repository.Plan != nil && !repository.Plan.Apply {
		comparison = repository.Plan
	}
	if comparison != nil {
		input.CheckedAt = comparison.At
		input.SourceDeleted = comparison.SourceDeleted
		input.ComparisonFailed = comparison.Error != ""
	}
	return input
}
