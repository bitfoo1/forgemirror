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

func openStatus(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*status.Store, func() error, error) {
	if !cfg.UI.Enabled && cfg.State.Database == "" {
		return nil, func() error { return nil }, nil
	}
	opts := status.Options{ScheduleInterval: cfg.Sync.Schedule, Redact: stateRedactor(cfg), OnError: func() {
		logger.ErrorContext(ctx, "persistent state write failed; sync durability is unavailable")
	}}
	var db *state.DB
	if cfg.State.Database != "" {
		var err error
		db, err = state.Open(ctx, cfg.State.Database, strings.TrimRight(cfg.Source.BaseURL, "/"))
		if err != nil {
			return nil, nil, err
		}
		opts.Persistence = db
		opts.ControlPersistence = db.Namespace("controls")
	}
	store := status.New(opts)
	if err := store.Restore(ctx); err != nil {
		if db != nil {
			err = errors.Join(err, db.Close())
		}
		return nil, nil, err
	}
	closeStore := func() error {
		if db != nil {
			return db.Close()
		}
		return nil
	}
	return store, closeStore, nil
}

// State contains only plans/results, never config or credentials. Scrub known
// secret values as a second boundary in case an upstream error echoes one.
func stateRedactor(cfg *config.Config) func([]byte) ([]byte, error) {
	secrets := []string{cfg.Webhook.Secret.Reveal(), cfg.Source.Token.Reveal()}
	for _, target := range cfg.Targets {
		secrets = append(secrets, target.Token.Reveal())
	}
	return state.Redactor(secrets)
}
