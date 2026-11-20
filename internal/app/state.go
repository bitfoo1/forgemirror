package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"go.bitfoo.de/forgemirror/internal/config"
	"go.bitfoo.de/forgemirror/internal/control"
	"go.bitfoo.de/forgemirror/internal/dispatch"
	"go.bitfoo.de/forgemirror/internal/filter"
	"go.bitfoo.de/forgemirror/internal/mirror"
	"go.bitfoo.de/forgemirror/internal/plan"
	"go.bitfoo.de/forgemirror/internal/registry"
	"go.bitfoo.de/forgemirror/internal/state"
	"go.bitfoo.de/forgemirror/internal/status"
	"go.bitfoo.de/forgemirror/internal/ui"
)

func checkLFS(ctx context.Context, cfg *config.Config) error {
	return mirror.NewGit(mirror.GitOptions{Binary: cfg.Mirror.GitBinary, Timeout: cfg.Mirror.Timeout}).CheckLFS(ctx)
}

func newDispatcher(
	cfg *config.Config,
	logger *slog.Logger,
	clients forgeClients,
	store *status.Store,
	observer dispatch.Observer,
	outcome func(context.Context, string, string, error),
) *dispatch.Dispatcher {
	var checkpoint func(context.Context, plan.Repo) error
	if store != nil {
		checkpoint = store.Checkpoint
	}
	return dispatch.New(dispatch.Options{
		Mirrorer: mirror.NewGit(
			mirror.GitOptions{Binary: cfg.Mirror.GitBinary, WorkDir: cfg.Mirror.WorkDir, Timeout: cfg.Mirror.Timeout},
		),
		Source:           cfg.Source,
		Targets:          cfg.Targets,
		ForgeSource:      clients.source,
		Forges:           clients.targets,
		Registry:         registry.New(),
		TransferTimeout:  cfg.Mirror.Timeout,
		MaxConcurrent:    cfg.Mirror.MaxConcurrent,
		Auto:             cfg.Sync.Auto,
		Logger:           logger,
		Observer:         observer,
		BeforeApply:      checkpoint,
		Retries:          store,
		Outcome:          outcome,
		VerifyAfterApply: store != nil,
		Mutation: func(repo, target string) (func(), error) {
			if store == nil {
				return func() {}, nil
			}
			return store.BeginMutation(repo, target)
		},
	})
}

func newController(
	cfg *config.Config,
	logger *slog.Logger,
	store *status.Store,
	flt *filter.Filter,
	clients forgeClients,
	dispatcher *dispatch.Dispatcher,
) *control.Controller {
	if store == nil {
		return nil
	}
	return control.New(control.Options{
		Source: clients.source, Filter: flt, Dispatcher: dispatcher,
		Pending: func() []string { return ui.Pending(store.Snapshot()) }, Logger: logger,
		CheckCloneURL: cfg.Source.CheckCloneURL,
		Store:         store,
		Targets:       cfg.Targets,
	})
}

func openStatus(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
) (*status.Store, *state.DB, func() error, error) {
	opts := status.Options{
		HistoryLimit:     cfg.State.HistoryLimit,
		HistoryRetention: cfg.State.HistoryRetention,
		ScheduleInterval: cfg.Sync.Schedule,
		Redact:           stateRedactor(cfg),
		OnError: func() {
			logger.ErrorContext(ctx, "persistent state write failed; sync durability is unavailable")
		},
	}
	db, err := state.Open(ctx, cfg.State.Database, strings.TrimRight(cfg.Source.BaseURL, "/"))
	if err != nil {
		return nil, nil, nil, err
	}
	opts.Persistence = db
	opts.ControlPersistence = db.Namespace("controls")
	store := status.New(opts)
	if err = store.Restore(ctx); err != nil {
		err = errors.Join(err, db.Close())
		return nil, nil, nil, err
	}
	return store, db, db.Close, nil
}

// State contains only plans/results, never config or credentials. Scrub known
// secret values as a second boundary in case an upstream error echoes one.
func stateRedactor(cfg *config.Config) func([]byte) ([]byte, error) {
	secrets := []string{
		cfg.Webhook.Secret.Reveal(),
		cfg.Source.Token.Reveal(),
		cfg.Notifications.WebhookURL.Reveal(),
		cfg.Notifications.Token.Reveal(),
	}
	for _, target := range cfg.Targets {
		secrets = append(secrets, target.Token.Reveal())
	}
	return state.Redactor(secrets)
}
