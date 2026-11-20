package ui

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"go.bitfoo.de/forgemirror/internal/status"
)

type historyView struct {
	status.Attempt

	Duration time.Duration
	Targets  []targetView
}

func (h *handler) history(w http.ResponseWriter, r *http.Request) {
	const pageSize = 20
	const maxPage = 50
	name := r.URL.Query().Get("repo")
	if !slices.ContainsFunc(h.opts.Store.Snapshot().Repos, func(repo status.Repo) bool { return repo.Name == name }) {
		http.NotFound(w, r)
		return
	}
	page := 1
	if text := r.URL.Query().Get("page"); text != "" {
		number, err := strconv.Atoi(text)
		if err != nil || number < 1 || number > maxPage {
			http.Error(w, "invalid history page", http.StatusBadRequest)
			return
		}
		page = number
	}
	entries, total := h.opts.Store.History(name, (page-1)*pageSize, pageSize)
	if page > 1 && len(entries) == 0 {
		http.NotFound(w, r)
		return
	}
	h.renderWith(w, r, "history", func(data *pageData) {
		data.HistoryRepo = name
		data.HistoryTotal = total
		if page > 1 {
			data.HistoryPrev = page - 1
		}
		if page*pageSize < total {
			data.HistoryNext = page + 1
		}
		for _, entry := range entries {
			data.History = append(
				data.History,
				historyView{
					Attempt:  entry,
					Duration: max(entry.Finished.Sub(entry.Started), 0),
					Targets:  targetViews(status.Repo{Plan: &entry.Plan, Targets: entry.Targets}),
				},
			)
		}
	})
}
