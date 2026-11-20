package dispatch_test

import (
	"errors"
	"log/slog"
	"testing"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/forge"
	"go.bitfoo.de/forgemirror/internal/repo"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/webhook"
)

func approvalPermit(store *status.Store) func(repo.Repository, *config.Target, bool) (func(), error) {
	return func(source repo.Repository, target *config.Target, publication bool) (func(), error) {
		destination, err := target.RemoteURL(source)
		if err != nil {
			return nil, err
		}
		return store.BeginPublication(
			source.FullName,
			target.Name,
			status.PublicationID(source.CloneURL, destination),
			publication,
		)
	}
}

func TestApprovalPreventsCreationAndArtifacts(t *testing.T) {
	t.Parallel()
	store := status.New(status.Options{})
	gh := &fakeForge{missing: true}
	m := &fakeMirrorer{}
	obs := &recordingObserver{}
	d := dispatch.New(
		dispatch.Options{
			Mirrorer:      m,
			Targets:       loadTargets(t, githubTarget),
			ForgeSource:   sourceRepo(),
			Forges:        map[string]forge.Target{"github": gh},
			MaxConcurrent: 2,
			Auto:          true,
			Logger:        slog.New(slog.DiscardHandler),
			Observer:      obs,
			Mutation:      approvalPermit(store),
		},
	)
	d.Dispatch(t.Context(), event("app"))
	shutdown(t, d)
	for _, call := range gh.snapshot() {
		if call != "Get" && call != "Labels" {
			t.Errorf("unapproved publication: %s", call)
		}
	}
	clones, pushes, _, _ := m.snapshot()
	if len(clones) != 0 || len(pushes) != 0 {
		t.Fatal("unapproved target reached Git/LFS")
	}
	results, _ := obs.snapshot()
	if len(results) != 1 || !errors.Is(results[0].err, status.ErrApprovalRequired) {
		t.Fatalf("results=%+v", results)
	}
}

func TestApprovalDoesNotBlockDeletion(t *testing.T) {
	t.Parallel()
	store := status.New(status.Options{})
	gh := &fakeForge{}
	d := dispatch.New(
		dispatch.Options{
			Mirrorer:      &fakeMirrorer{},
			Targets:       loadTargets(t, githubTarget+"    delete: true\n"),
			ForgeSource:   sourceRepo(),
			Forges:        map[string]forge.Target{"github": gh},
			MaxConcurrent: 2,
			Auto:          true,
			Logger:        slog.New(slog.DiscardHandler),
			Mutation:      approvalPermit(store),
		},
	)
	d.Dispatch(t.Context(), withKind(event("app"), webhook.EventDelete))
	shutdown(t, d)
	found := false
	for _, call := range gh.snapshot() {
		found = found || call == "Delete"
	}
	if !found {
		t.Fatal("cleanup required first-publication authorization")
	}
}
